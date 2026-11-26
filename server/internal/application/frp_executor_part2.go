package application

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func frpsBinaryForRemote(ctx context.Context, client sshSessionClient, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("FRPS_BINARY_NOT_CONFIGURED")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("FRPS_BINARY_NOT_FOUND:%s: %w", path, err)
	}
	if !info.IsDir() {
		if info.Mode()&0o111 == 0 {
			return "", errors.New("FRPS_BINARY_NOT_EXECUTABLE")
		}
		return path, nil
	}
	arch, err := remoteLinuxArch(ctx, client)
	if err != nil {
		return "", err
	}
	binaryPath, err := frpsBinaryForArch(path, arch)
	if err != nil {
		return "", err
	}
	info, err = os.Stat(binaryPath)
	if err != nil {
		return "", fmt.Errorf("FRPS_BINARY_NOT_FOUND:%s: %w", binaryPath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("FRPS_BINARY_IS_DIRECTORY:%s", binaryPath)
	}
	if info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("FRPS_BINARY_NOT_EXECUTABLE:%s", binaryPath)
	}
	return binaryPath, nil
}

func remoteLinuxArch(ctx context.Context, client sshSessionClient) (string, error) {
	output, err := remoteOutput(ctx, client, "uname -m")
	if err != nil {
		return "", fmt.Errorf("REMOTE_ARCH_DETECT_FAILED: %w", err)
	}
	return normalizeLinuxArch(output)
}

func normalizeLinuxArch(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "x86_64", "amd64":
		return "amd64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("FRPS_REMOTE_ARCH_UNSUPPORTED:%s", strings.TrimSpace(value))
	}
}

func frpsBinaryForArch(bundleDir string, arch string) (string, error) {
	for _, candidate := range []string{
		filepath.Join(bundleDir, "frps-linux-"+arch),
		filepath.Join(bundleDir, "linux-"+arch, "frps"),
		filepath.Join(bundleDir, "frps"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("FRPS_BINARY_FOR_REMOTE_ARCH_NOT_FOUND:%s:%s", bundleDir, arch)
}

func frpTraefikNetwork(input FrpTunnelExecutionInput) string {
	return firstNonEmptyString(strings.TrimSpace(input.TraefikNetwork), "iquops-public")
}

func frpTraefikNetworkCheckCommand(network string) string {
	return "test \"$(docker network inspect " + shellQuote(network) + " --format '{{.Driver}}|{{.Scope}}' 2>/dev/null)\" = 'overlay|swarm'"
}

func frpsTraefikLabels(input FrpTunnelExecutionInput) []string {
	runID := input.Run.ID
	prefix := traefikResourceName("iquops-" + runID)
	labels := []string{
		"traefik.enable=true",
		"traefik.swarm.network=" + frpTraefikNetwork(input),
	}
	controlMapping := frpControlMapping(input)
	controlEntrypoint := frpMappingEntrypoint(controlMapping)
	controlHost := firstNonEmptyString(controlMapping.PublicDomain, serverPublicHost(input.Server))
	labels = append(labels, traefikHTTPRouterLabels(
		prefix+"-control",
		prefix+"-control",
		traefikHTTPRule(controlHost, "/~!frp"),
		controlEntrypoint, input.CertResolver,
		input.Run.FrpsBindPort,
		10000,
	)...)

	mappings := append([]domain.FrpTunnelMapping{}, input.Run.Mappings...)
	sort.SliceStable(mappings, func(i, j int) bool {
		return mappings[i].Name < mappings[j].Name
	})
	for _, mapping := range mappings {
		if mapping.PublicDomain == "" {
			continue
		}
		name := traefikResourceName(prefix + "-" + mapping.Name)
		host := mapping.PublicDomain
		labels = append(labels, traefikHTTPRouterLabels(
			name,
			name,
			traefikHTTPRule(host, frpMappingPublicPathPrefix(mapping)),
			frpMappingEntrypoint(mapping), input.CertResolver,
			frpMappingProxyPort(mapping),
			100+len(frpMappingPublicPathPrefix(mapping)),
		)...)
	}
	return labels
}

func traefikHTTPRouterLabels(router string, service string, rule string, entrypoint string, resolver string, targetPort int, priority int) []string {
	labels := []string{
		"traefik.http.routers." + router + ".rule=" + rule,
		"traefik.http.routers." + router + ".entrypoints=" + entrypoint,
		"traefik.http.routers." + router + ".service=" + service,
		"traefik.http.routers." + router + ".priority=" + strconv.Itoa(priority),
		"traefik.http.services." + service + ".loadbalancer.server.port=" + strconv.Itoa(targetPort),
	}
	if entrypoint == "websecure" {
		labels = append(labels,
			"traefik.http.routers."+router+".tls=true",
			"traefik.http.routers."+router+".tls.certresolver="+managedTraefikResolver(resolver),
		)
	}
	return labels
}

func traefikHTTPRule(host string, pathPrefix string) string {
	parts := []string{}
	if host = strings.TrimSpace(host); host != "" {
		parts = append(parts, "Host(`"+strings.ReplaceAll(host, "`", "")+"`)")
	}
	pathPrefix = frpPublicPathForRule(pathPrefix)
	if pathPrefix != "" {
		parts = append(parts, "PathPrefix(`"+strings.ReplaceAll(pathPrefix, "`", "")+"`)")
	}
	if len(parts) == 0 {
		return "PathPrefix(`/`)"
	}
	return strings.Join(parts, " && ")
}

func frpPublicPathForRule(pathPrefix string) string {
	pathPrefix, ok := normalizeTunnelPublicPathPrefix(pathPrefix)
	if !ok || pathPrefix == "" {
		return "/"
	}
	return pathPrefix
}

func traefikResourceName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	previousDash := false
	for _, char := range value {
		valid := (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')
		if valid {
			builder.WriteRune(char)
			previousDash = false
			continue
		}
		if !previousDash {
			builder.WriteByte('-')
			previousDash = true
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "iquops-frp"
	}
	if len(result) > 80 {
		return strings.Trim(result[:80], "-")
	}
	return result
}

func frpsDockerfile() string {
	return "FROM scratch\nCOPY frps /frps\nENTRYPOINT [\"/frps\"]\n"
}

func frpsDockerignore() string {
	return "*\n!Dockerfile\n!.dockerignore\n!frps\n"
}

func frpsConfig(input FrpTunnelExecutionInput) string {
	ports := make([]string, 0, len(input.Run.Mappings))
	for _, mapping := range input.Run.Mappings {
		ports = append(ports, "{ single = "+strconv.Itoa(mapping.FrpsProxyPort)+" }")
	}
	return strings.Join([]string{
		"bindPort = " + strconv.Itoa(input.Run.FrpsBindPort),
		`log.to = "console"`,
		`auth.method = "token"`,
		`auth.tokenSource.type = "file"`,
		`auth.tokenSource.file.path = "/etc/frp/token"`,
		`transport.tls.force = true`,
		"allowPorts = [" + strings.Join(ports, ", ") + "]",
		"",
	}, "\n")
}

func frpcConfig(input FrpTunnelExecutionInput) string {
	lines := []string{
		"serverAddr = " + tomlQuote(frpControlHost(input)),
		"serverPort = " + strconv.Itoa(frpControlPort(input)),
	}
	if dnsServer := strings.TrimSpace(input.FrpcDNSServer); dnsServer != "" {
		lines = append(lines, "dnsServer = "+tomlQuote(dnsServer))
	}
	lines = append(lines,
		`log.to = "console"`,
		`auth.method = "token"`,
		`auth.token = "{{ .Envs.FRP_TOKEN }}"`,
		"transport.protocol = "+tomlQuote(frpControlTransportProtocol(input)),
		"transport.tls.enable = true",
		"",
	)
	mappings := append([]domain.FrpTunnelMapping{}, input.Run.Mappings...)
	sort.SliceStable(mappings, func(i, j int) bool {
		return mappings[i].Name < mappings[j].Name
	})
	for _, mapping := range mappings {
		lines = append(lines,
			"[[proxies]]",
			"name = "+tomlQuote(mapping.Name),
			`type = "tcp"`,
			"remotePort = "+strconv.Itoa(frpMappingProxyPort(mapping)),
		)
		if mapping.TargetType == domain.FrpTunnelMappingTargetHTTPProbe {
			lines = append(lines,
				"[proxies.plugin]",
				`type = "http2http"`,
				"localAddr = "+tomlQuote(net.JoinHostPort(mapping.LocalHost, strconv.Itoa(mapping.LocalPort))),
				"[proxies.plugin.requestHeaders.set]",
				frpProbeTunnelHeader+" = "+tomlQuote(input.Run.TunnelID),
				frpProbeRunHeader+" = "+tomlQuote(input.Run.ID),
				frpProbeMappingHeader+" = "+tomlQuote(mapping.Name),
			)
		} else {
			lines = append(lines, "localIP = "+tomlQuote(mapping.LocalHost), "localPort = "+strconv.Itoa(mapping.LocalPort))
		}
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func frpControlMapping(input FrpTunnelExecutionInput) domain.FrpTunnelMapping {
	for _, mapping := range input.Run.Mappings {
		if mapping.PublicDomain != "" {
			return mapping
		}
	}
	return domain.FrpTunnelMapping{}
}

func frpControlHost(input FrpTunnelExecutionInput) string {
	mapping := frpControlMapping(input)
	return firstNonEmptyString(mapping.PublicDomain, serverPublicHost(input.Server))
}

func frpControlPort(input FrpTunnelExecutionInput) int {
	mapping := frpControlMapping(input)
	if mapping.PublicDomain == "" {
		return input.Run.FrpsBindPort
	}
	return traefikExternalPort(input.Server, frpMappingPublicScheme(mapping), input.TraefikHTTPPort, input.TraefikHTTPSPort)
}

func frpControlTransportProtocol(input FrpTunnelExecutionInput) string {
	mapping := frpControlMapping(input)
	if mapping.PublicDomain == "" {
		return "tcp"
	}
	if frpMappingPublicScheme(mapping) == "https" {
		return "wss"
	}
	return "websocket"
}

func executableFrpcPath(input FrpTunnelExecutionInput) (string, error) {
	path := strings.TrimSpace(input.FrpcPath)
	if path == "" {
		var err error
		path, err = exec.LookPath("frpc")
		if err != nil {
			return "", errors.New("FRPC_BINARY_NOT_CONFIGURED")
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("FRPC_BINARY_NOT_FOUND:%s: %w", path, err)
	}
	if info.IsDir() {
		return "", errors.New("FRPC_BINARY_IS_DIRECTORY")
	}
	if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return "", errors.New("FRPC_BINARY_NOT_EXECUTABLE")
	}
	return path, nil
}

func localFrpTunnelDir(input FrpTunnelExecutionInput) string {
	runID := sanitizeRuntimeName(input.Run.ID)
	stateDir := firstNonEmptyString(input.StateDir, filepath.Join(input.RepositoryRoot, ".local", "state", "iquops"))
	if runID == "" || stateDir == "" {
		return ""
	}
	return filepath.Join(stateDir, "frp-tunnels", runID)
}

func httpGetWithRetry(ctx context.Context, targetURL string, attempts int, delay time.Duration) error {
	if strings.TrimSpace(targetURL) == "" {
		return errors.New("HTTP_ENDPOINT_REQUIRED")
	}
	client := httpClientForReadinessProbe(targetURL)
	return retryFrpEndpoint(ctx, attempts, delay, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != 502 && resp.StatusCode != 503 && resp.StatusCode != 504 {
				return nil
			}
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return err
	})
}

func retryFrpEndpoint(ctx context.Context, attempts int, delay time.Duration, check func() error) error {
	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		if last = check(); last == nil {
			return nil
		}
		if attempt+1 == attempts {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return last
}

func httpClientForReadinessProbe(targetURL string) http.Client {
	client := http.Client{Timeout: 2 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(targetURL)), "https://") {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- readiness only; ACME may still be issuing the public certificate.
	}
	client.Transport = transport
	return client
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func tomlQuote(value string) string {
	escaped := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\r", "\\r").Replace(value)
	return `"` + escaped + `"`
}

func serverPublicHost(server map[string]any) string {
	return firstNonEmptyString(stringValue(server["publicHost"]), stringValue(server["privateHost"]))
}

func withFrpLogStep(input FrpTunnelExecutionInput) FrpTunnelExecutionInput {
	if input.LogStep == nil {
		input.LogStep = func(string, string, string, string) {}
	}
	return input
}
