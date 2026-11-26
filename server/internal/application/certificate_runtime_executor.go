package application

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 证书探针文件只用于触发并验证 ACME 签发，验证完成后立即从网关移除。
const certificateProbePrefix = "certificate-probe-"

// 证书校验同时需要远端命令与直连 TLS，因此要求客户端支持会话与拨号。
type certificateSSHClient interface {
	sshSessionClient
	DialContext(context.Context, string, string) (net.Conn, error)
}

type SystemCertificateVerifier struct{}

func NewSystemCertificateVerifier() *SystemCertificateVerifier {
	return &SystemCertificateVerifier{}
}

func (e *SystemCertificateVerifier) Verify(ctx context.Context, input CertificateVerifyInput) (domain.Object, error) {
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	if len(input.Domains) == 0 {
		return nil, errors.New("CERTIFICATE_DOMAIN_REQUIRED")
	}
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return nil, errors.New("CERTIFICATE_SSH_UNAVAILABLE")
	}
	defer func() { _ = client.Close() }()
	if err := inspectCertificateGateway(ctx, client, input); err != nil {
		return nil, err
	}
	name := certificateProbePrefix + routeRevision(domain.Object{"domains": input.Domains, "certificate": input.Certificate["id"]})
	path := traefikRemoteRoot + "/dynamic/" + name + ".yml"
	content, err := buildCertificateProbeConfig(name, input)
	if err != nil {
		return nil, err
	}
	input.Log("traefik", "certificate-request", "info", "已在网关声明 ACME 签发入口，等待 Traefik 申请证书")
	if err := writeRemoteFile(ctx, client, path, string(content)); err != nil {
		return nil, errors.New("CERTIFICATE_PROBE_UPLOAD_FAILED")
	}
	result, verifyErr := waitCertificateIssued(ctx, client, input, name)
	// 无论成功失败都撤销探针配置，避免长期暴露占位路由。
	cleanupCtx, stopCleanup := context.WithTimeout(context.Background(), 20*time.Second)
	defer stopCleanup()
	if cleanupErr := remoteRun(cleanupCtx, client, "rm -f "+shellQuote(path)); cleanupErr != nil && verifyErr == nil {
		return nil, errors.New("CERTIFICATE_PROBE_CLEANUP_FAILED")
	}
	if verifyErr != nil {
		return nil, verifyErr
	}
	return result, nil
}

func inspectCertificateGateway(ctx context.Context, client sshSessionClient, input CertificateVerifyInput) error {
	status, err := readRemoteSwarmStatus(ctx, client, input.Network)
	if err != nil || !status.Active || status.Role != "manager" {
		return errors.New("CERTIFICATE_SWARM_MANAGER_REQUIRED")
	}
	if input.ClusterID != "" {
		cluster, err := remoteOutput(ctx, client, "docker info --format '{{.Swarm.Cluster.ID}}'")
		if err != nil || cluster != input.ClusterID {
			return errors.New("CERTIFICATE_SWARM_CLUSTER_MISMATCH")
		}
	}
	gateway, err := inspectCertificateTraefik(ctx, client, input.TraefikName)
	if err != nil {
		return errors.New("CERTIFICATE_TRAEFIK_UNAVAILABLE")
	}
	labels := objectMapFromAny(objectMapFromAny(gateway["Spec"])["Labels"])
	if stringValue(labels["iquops.owner"]) != "iquops" || stringValue(labels["iquops.component"]) != "traefik_gateway" {
		return errors.New("CERTIFICATE_TRAEFIK_UNMANAGED")
	}
	return nil
}

func inspectCertificateTraefik(ctx context.Context, client sshSessionClient, name string) (domain.Object, error) {
	output, err := remoteOutput(ctx, client, "docker service inspect "+shellQuote(name))
	if err != nil {
		return nil, err
	}
	var items []domain.Object
	if err := json.Unmarshal([]byte(output), &items); err != nil || len(items) != 1 {
		return nil, errors.New("CERTIFICATE_TRAEFIK_INSPECT_INVALID")
	}
	return items[0], nil
}

func buildCertificateProbeConfig(name string, input CertificateVerifyInput) ([]byte, error) {
	if len(input.Domains) == 0 {
		return nil, errors.New("CERTIFICATE_DOMAIN_REQUIRED")
	}
	routers, services := domain.Object{}, domain.Object{}
	for index, host := range input.Domains {
		router := name + "-" + strconv.Itoa(index)
		service := router + "-sink"
		routers[router] = domain.Object{
			"rule":        "Host(" + strconv.Quote(host) + ")",
			"entryPoints": []string{"websecure"},
			"service":     service,
			"tls": domain.Object{
				"certResolver": input.ResolverName,
				"domains":      []domain.Object{{"main": input.Domains[0], "sans": input.Domains[1:]}},
			},
		}
		// 证书签发完成后请求会落到占位服务，因此只返回固定状态，不转发到真实后端。
		services[service] = domain.Object{
			"loadBalancer": domain.Object{
				"servers": []domain.Object{{"url": "http://127.0.0.1:" + strconv.Itoa(input.HTTPPort)}},
			},
		}
	}
	return json.MarshalIndent(domain.Object{
		"http": domain.Object{"routers": routers, "services": services},
	}, "", "  ")
}

func waitCertificateIssued(ctx context.Context, client certificateSSHClient, input CertificateVerifyInput, name string) (domain.Object, error) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	lastCode := "CERTIFICATE_NOT_ISSUED"
	for {
		if covers, expiresAt, issuer, err := probeCertificateChain(ctx, client, input, name); err == nil && covers {
			return domain.Object{"expiresAt": expiresAt, "issuerName": issuer, "domains": input.Domains}, nil
		} else if err != nil {
			lastCode = stableErrorCode(err, "CERTIFICATE_NOT_ISSUED")
		}
		select {
		case <-ctx.Done():
			return nil, errors.New(lastCode)
		case <-ticker.C:
		}
	}
}

func probeCertificateChain(ctx context.Context, client certificateSSHClient, input CertificateVerifyInput, name string) (bool, string, string, error) {
	port := input.HTTPSPort
	if port <= 0 {
		port = 443
	}
	expiry, issuer := "", ""
	for _, host := range input.Domains {
		conn, err := dialCertificateTLS(ctx, client, host, port)
		if err != nil {
			return false, "", "", errors.New("CERTIFICATE_TLS_NOT_READY")
		}
		state := conn.ConnectionState()
		_ = conn.Close()
		if len(state.PeerCertificates) == 0 {
			return false, "", "", errors.New("CERTIFICATE_CHAIN_EMPTY")
		}
		leaf := state.PeerCertificates[0]
		// Traefik may keep one certificate per SNI even when the request uses a
		// common domain set.  Each endpoint must cover its own hostname; requiring
		// every leaf to contain every SAN incorrectly rejects that valid layout.
		if err := leaf.VerifyHostname(host); err != nil {
			return false, "", "", errors.New("CERTIFICATE_DOMAIN_MISMATCH")
		}
		value := leaf.NotAfter.UTC().Format(time.RFC3339)
		if expiry == "" || value < expiry {
			expiry = value
		}
		issuer = certificateIssuerName(leaf)
	}
	return true, expiry, issuer, nil
}

func dialCertificateTLS(ctx context.Context, client certificateSSHClient, host string, port int) (*tls.Conn, error) {
	raw, err := client.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	conn := tls.Client(raw, certificateTLSConfig(host))
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return conn, nil
}

func certificateTLSConfig(host string) *tls.Config {
	// 通过 SSH 连接网关本机端口，仍按真实域名、系统信任链和有效期执行标准校验。
	return &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
}

func missingCertificateDomains(certificate *x509.Certificate, domains []string) []string {
	missing := []string{}
	for _, host := range domains {
		if err := certificate.VerifyHostname(host); err != nil {
			missing = append(missing, host)
		}
	}
	sort.Strings(missing)
	return missing
}

func certificateIssuerName(certificate *x509.Certificate) string {
	if len(certificate.Issuer.Organization) > 0 {
		return strings.TrimSpace(certificate.Issuer.Organization[0])
	}
	return strings.TrimSpace(certificate.Issuer.CommonName)
}

// httpsChallengeReachable 用于在申请前确认 HTTP-01 入口可达，避免长时间无效等待。
func httpsChallengeReachable(ctx context.Context, client certificateSSHClient, host string, port int) bool {
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(callCtx context.Context, _, _ string) (net.Conn, error) {
			return client.DialContext(callCtx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		},
		TLSHandshakeTimeout: 8 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			ServerName:         host,
			InsecureSkipVerify: true,
		},
	}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+net.JoinHostPort(host, strconv.Itoa(port))+"/", nil)
	if err != nil {
		return false
	}
	request.Host = host
	response, err := (&http.Client{Transport: transport, Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return false
	}
	defer func() { _ = response.Body.Close() }()
	return true
}
