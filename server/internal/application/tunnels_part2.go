package application

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) publicFrpTunnel(tunnel domain.FrpTunnel, state domain.State, hitCount int) domain.Object {
	server := findObject(state.Servers, tunnel.ServerID)
	runs := domain.FrpTunnelRunsFor(state.FrpTunnelRuns, tunnel.ID)
	legacyIngress := len(runs) > 0 && runs[0].Status.NeedsCleanup() && !runs[0].ManagedFrps
	result := domain.Object{
		"id":            tunnel.ID,
		"name":          tunnel.Name,
		"serverId":      tunnel.ServerID,
		"frpsBindPort":  tunnel.FrpsBindPort,
		"mappings":      publicFrpTunnelMappings(tunnel.Mappings, server, r.Config.TraefikHTTPPort, r.Config.TraefikHTTPSPort, legacyIngress),
		"status":        frpTunnelStatusStopped,
		"runCount":      0,
		"probeHitCount": hitCount,
		"createdAt":     tunnel.CreatedAt,
		"updatedAt":     tunnel.UpdatedAt,
	}
	if server != nil {
		result["server"] = tunnelServerSummary(server)
	}
	result["runCount"] = len(runs)
	if len(runs) > 0 {
		result["latestRun"] = publicFrpTunnelRun(runs[0])
		result["status"] = publicTunnelStatus(runs[0].Status)
		result["lastRunAt"] = runs[0].UpdatedAt
	}
	return result
}

func publicFrpTunnelRun(run domain.FrpTunnelRun) domain.Object {
	result := domain.Object{
		"id":        run.ID,
		"tunnelId":  run.TunnelID,
		"status":    run.Status,
		"createdAt": run.CreatedAt,
		"updatedAt": run.UpdatedAt,
	}
	if run.Status.NeedsCleanup() && !run.ManagedFrps {
		result["legacyIngress"] = true
	}
	if run.ErrorSummary != "" {
		fallback := "FRP_TUNNEL_OPERATION_FAILED"
		if run.Status == frpTunnelStatusCancelled {
			fallback = "OPERATION_CANCELLED"
		}
		result["errorSummary"] = stableErrorCode(errors.New(run.ErrorSummary), fallback)
	}
	optional := map[string]string{
		"startOperationRunId": run.StartOperationRunID, "stopOperationRunId": run.StopOperationRunID,
		"startedAt": run.StartedAt, "stoppedAt": run.StoppedAt, "failedAt": run.FailedAt, "lastVerifiedAt": run.LastVerifiedAt,
	}
	for key, value := range optional {
		if value != "" {
			result[key] = value
		}
	}
	return result
}

func publicTunnelStatus(status domain.FrpTunnelStatus) domain.FrpTunnelStatus {
	if status == frpTunnelStatusCancelled {
		return frpTunnelStatusStopped
	}
	return status
}

func publicFrpTunnelDraft(draft frpTunnelDraft) domain.Object {
	mappings := make([]domain.Object, 0, len(draft.Mappings))
	for _, mapping := range draft.Mappings {
		item := domain.Object{
			"targetType": mapping.TargetType, "name": mapping.Name, "frpsProxyPort": mapping.FrpsProxyPort,
			"publicPathPrefix": mapping.PublicPathPrefix, "entrypoint": mapping.Entrypoint, "publicScheme": mapping.PublicScheme,
		}
		if mapping.PublicDomain != "" {
			item["publicDomain"] = mapping.PublicDomain
		}
		if mapping.TargetType == domain.FrpTunnelMappingTargetLocalService {
			item["localHost"] = mapping.LocalHost
			item["localPort"] = mapping.LocalPort
		}
		mappings = append(mappings, item)
	}
	return domain.Object{
		"name":         draft.Name,
		"serverId":     draft.ServerID,
		"frpsBindPort": draft.FrpsBindPort,
		"mappings":     mappings,
	}
}

func publicFrpTunnelMappings(mappings []domain.FrpTunnelMapping, server domain.Object, httpPort int, httpsPort int, legacyIngress bool) []domain.Object {
	items := make([]domain.Object, 0, len(mappings))
	for _, mapping := range mappings {
		item, _ := domain.EncodeObject(mapping)
		if server != nil {
			item["publicUrl"] = frpTunnelPublicURL(server, mapping, httpPort, httpsPort, legacyIngress)
		}
		items = append(items, item)
	}
	return items
}

func normalizeFrpTunnelRequest(input domain.FrpTunnelInput, probeHost string, probePort int) (frpTunnelDraft, []string) {
	draft := frpTunnelDraft{
		Name:         strings.TrimSpace(input.Name),
		ServerID:     strings.TrimSpace(input.ServerID),
		FrpsBindPort: input.FrpsBindPort,
	}
	if draft.FrpsBindPort == 0 {
		draft.FrpsBindPort = 7000
	}
	errorsList := []string{}
	if draft.Name == "" || len([]rune(draft.Name)) > 80 || strings.ContainsAny(draft.Name, "\r\n\t") {
		errorsList = append(errorsList, "FRP_TUNNEL_NAME_INVALID")
	}
	if draft.ServerID == "" {
		errorsList = append(errorsList, "SERVER_ID_REQUIRED")
	}
	if !validPublishedPort(draft.FrpsBindPort) {
		errorsList = append(errorsList, "FRPS_BIND_PORT_INVALID")
	}
	rawMappings := input.Mappings
	if len(rawMappings) == 0 {
		errorsList = append(errorsList, "FRP_TUNNEL_MAPPING_REQUIRED")
	}
	if len(rawMappings) > 20 {
		errorsList = append(errorsList, "FRP_TUNNEL_MAPPING_LIMIT_EXCEEDED")
	}
	names := map[string]bool{}
	proxyPorts := map[int]bool{}
	publicRoutes := map[string]bool{}
	for _, raw := range rawMappings {
		name := strings.TrimSpace(raw.Name)
		targetType := raw.TargetType
		localHost := strings.TrimSpace(raw.LocalHost)
		localPort := raw.LocalPort
		if targetType == domain.FrpTunnelMappingTargetHTTPProbe {
			localHost = firstNonEmptyString(probeHost, "127.0.0.1")
			localPort = numberOrDefault(probePort, 17990)
		} else if targetType != domain.FrpTunnelMappingTargetLocalService {
			errorsList = append(errorsList, "FRP_TUNNEL_MAPPING_TARGET_INVALID")
		}
		frpsProxyPort := raw.FrpsProxyPort
		publicDomain := normalizeTunnelPublicDomain(raw.PublicDomain)
		publicPathPrefix, pathOK := normalizeTunnelPublicPathPrefix(raw.PublicPathPrefix)
		entrypoint := strings.TrimSpace(raw.Entrypoint)
		publicScheme := strings.TrimSpace(raw.PublicScheme)
		if entrypoint == "" {
			entrypoint = "web"
		}
		if publicScheme == "" {
			if entrypoint == "websecure" {
				publicScheme = "https"
			} else {
				publicScheme = "http"
			}
		}
		if !frpTunnelMappingNamePattern.MatchString(name) {
			errorsList = append(errorsList, "FRP_TUNNEL_MAPPING_NAME_INVALID")
		}
		if names[name] {
			errorsList = append(errorsList, "FRP_TUNNEL_MAPPING_NAME_DUPLICATE")
		}
		names[name] = true
		if localHost != "127.0.0.1" && localHost != "localhost" && localHost != "::1" {
			errorsList = append(errorsList, "FRP_TUNNEL_LOCAL_HOST_INVALID")
		}
		if localPort < 1 || localPort > 65535 {
			errorsList = append(errorsList, "FRP_TUNNEL_LOCAL_PORT_INVALID")
		}
		if !validPublishedPort(frpsProxyPort) {
			errorsList = append(errorsList, "FRP_TUNNEL_FRPS_PROXY_PORT_INVALID")
		}
		if frpsProxyPort == draft.FrpsBindPort {
			errorsList = append(errorsList, "FRP_TUNNEL_FRPS_PROXY_PORT_EQUALS_BIND_PORT")
		}
		if proxyPorts[frpsProxyPort] {
			errorsList = append(errorsList, "FRP_TUNNEL_FRPS_PROXY_PORT_DUPLICATE")
		}
		proxyPorts[frpsProxyPort] = true
		if publicDomain != "" && !validTunnelPublicDomain(publicDomain) {
			errorsList = append(errorsList, "FRP_TUNNEL_PUBLIC_DOMAIN_INVALID")
		}
		if !pathOK {
			errorsList = append(errorsList, "FRP_TUNNEL_PUBLIC_PATH_PREFIX_INVALID")
		}
		if entrypoint != "web" && entrypoint != "websecure" {
			errorsList = append(errorsList, "FRP_TUNNEL_ENTRYPOINT_INVALID")
		}
		if publicScheme != "http" && publicScheme != "https" {
			errorsList = append(errorsList, "FRP_TUNNEL_PUBLIC_SCHEME_INVALID")
		}
		if publicScheme == "https" && publicDomain == "" {
			errorsList = append(errorsList, "FRP_TUNNEL_PUBLIC_DOMAIN_REQUIRED_FOR_HTTPS")
		}
		if (entrypoint == "web" && publicScheme != "http") || (entrypoint == "websecure" && publicScheme != "https") {
			errorsList = append(errorsList, "FRP_TUNNEL_ENTRYPOINT_SCHEME_MISMATCH")
		}
		routeKey := entrypoint + "|" + publicDomain + "|" + publicPathPrefix
		if publicDomain != "" && publicRoutes[routeKey] {
			errorsList = append(errorsList, "TRAEFIK_ENTRYPOINT_CONFLICT")
		}
		publicRoutes[routeKey] = true
		draft.Mappings = append(draft.Mappings, domain.FrpTunnelMapping{
			Name: name, TargetType: targetType, LocalHost: localHost, LocalPort: localPort, FrpsProxyPort: frpsProxyPort,
			PublicDomain: publicDomain, PublicPathPrefix: publicPathPrefix, Entrypoint: entrypoint, PublicScheme: publicScheme,
		})
	}
	return draft, uniqueStrings(errorsList)
}

func frpTunnelAssetBlockers(state domain.State, mappings []domain.FrpTunnelMapping) []string {
	reasons := []string{}
	for _, mapping := range mappings {
		if mapping.PublicDomain == "" {
			if frpMappingPublicScheme(mapping) == "https" {
				reasons = append(reasons, "FRP_TUNNEL_PUBLIC_DOMAIN_REQUIRED_FOR_HTTPS")
			}
			continue
		}
		registered := false
		for _, asset := range state.Domains {
			if normalizeTunnelPublicDomain(stringValue(asset["name"])) == mapping.PublicDomain {
				if jenkinsDomainInUse(state, stringValue(asset["id"]), "") || redirectBlocksBackend(asset, frpMappingPublicScheme(mapping)) {
					reasons = append(reasons, "DOMAIN_REDIRECT_HOST_IN_USE")
				}
				registered = true
				break
			}
		}
		if !registered {
			reasons = append(reasons, "FRP_TUNNEL_PUBLIC_DOMAIN_NOT_IN_ASSETS")
		}
	}
	return uniqueStrings(reasons)
}

func frpUsesTraefik(mappings []domain.FrpTunnelMapping) bool {
	for _, mapping := range mappings {
		if mapping.PublicDomain != "" {
			return true
		}
	}
	return false
}

func duplicateTunnelName(tunnels []domain.FrpTunnel, serverID string, name string, exceptID string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, tunnel := range tunnels {
		if tunnel.ID != exceptID && tunnel.ServerID == serverID && strings.ToLower(strings.TrimSpace(tunnel.Name)) == name {
			return true
		}
	}
	return false
}

func frpTunnelHasBlockingRun(runs []domain.FrpTunnelRun, tunnelID string) bool {
	for _, run := range runs {
		if run.TunnelID == tunnelID && run.Status.IsActive() {
			return true
		}
	}
	return false
}

func latestActiveFrpTunnelRun(runs []domain.FrpTunnelRun, tunnelID string) *domain.FrpTunnelRun {
	for _, run := range domain.FrpTunnelRunsFor(runs, tunnelID) {
		if run.Status.IsActive() {
			copy := run
			return &copy
		}
	}
	return nil
}

func cloneFrpTunnelMappings(mappings []domain.FrpTunnelMapping) []domain.FrpTunnelMapping {
	return append([]domain.FrpTunnelMapping{}, mappings...)
}

func publicTunnelSortTime(item domain.Object) string {
	return firstNonEmptyString(stringValue(item["lastRunAt"]), stringValue(item["updatedAt"]))
}

func normalizeTunnelPublicDomain(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimPrefix(value, "https://")
	return strings.Trim(value, ".")
}

func validTunnelPublicDomain(value string) bool {
	if value == "" || strings.ContainsAny(value, "/:\\") || len(value) > 253 {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" || len(part) > 63 || strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return false
		}
		for _, char := range part {
			if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func normalizeTunnelPublicPathPrefix(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "/", true
	}
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, " \t\r\n") {
		return value, false
	}
	for len(value) > 1 && strings.HasSuffix(value, "/") {
		value = strings.TrimSuffix(value, "/")
	}
	return value, true
}

func validPublishedPort(port int) bool {
	return port >= 1024 && port <= 65535
}

func tunnelServerSummary(server domain.Object) domain.Object {
	return domain.Object{
		"id": stringValue(server["id"]), "name": firstNonEmptyString(stringValue(server["name"]), stringValue(server["id"])), "publicHost": stringValue(server["publicHost"]),
	}
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func frpTunnelContainerName(runID string) string {
	return "iquops-frps-" + sanitizeRuntimeName(runID)
}

func frpTunnelRemoteDir(runID string) string {
	return "/opt/iquops/tunnels/" + sanitizeRuntimeName(runID)
}

func sanitizeRuntimeName(value string) string {
	return strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-").Replace(value)
}

func (r *Runtime) frpTunnelPublicURL(server domain.Object, mapping domain.FrpTunnelMapping) string {
	return frpTunnelPublicURL(server, mapping, r.Config.TraefikHTTPPort, r.Config.TraefikHTTPSPort, false)
}

func frpTunnelPublicURL(server domain.Object, mapping domain.FrpTunnelMapping, fallbackHTTPPort int, fallbackHTTPSPort int, legacyIngress bool) string {
	host := firstNonEmptyString(mapping.PublicDomain, serverPublicHost(server))
	if host == "" {
		return ""
	}
	scheme := frpMappingPublicScheme(mapping)
	port := traefikExternalPort(server, scheme, fallbackHTTPPort, fallbackHTTPSPort)
	if mapping.PublicDomain == "" && !legacyIngress {
		scheme = "http"
		port = mapping.FrpsProxyPort
	}
	host = strings.Trim(host, "[]")
	if (scheme == "http" && port != 80) || (scheme == "https" && port != 443) {
		host = net.JoinHostPort(host, strconv.Itoa(port))
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	pathPrefix := frpMappingPublicPathPrefix(mapping)
	if pathPrefix == "/" {
		pathPrefix = ""
	}
	return scheme + "://" + host + pathPrefix
}

func traefikExternalPort(server domain.Object, scheme string, fallbackHTTPPort int, fallbackHTTPSPort int) int {
	if fallbackHTTPPort <= 0 {
		fallbackHTTPPort = 80
	}
	if fallbackHTTPSPort <= 0 {
		fallbackHTTPSPort = 443
	}
	metrics := objectMapFromAny(objectMapFromAny(server["health"])["metrics"])
	if scheme == "https" {
		return numberOrDefault(metrics["traefikHttpsPort"], fallbackHTTPSPort)
	}
	return numberOrDefault(metrics["traefikHttpPort"], fallbackHTTPPort)
}

func frpMappingProxyPort(mapping domain.FrpTunnelMapping) int {
	return mapping.FrpsProxyPort
}

func frpMappingPublicPathPrefix(mapping domain.FrpTunnelMapping) string {
	value, ok := normalizeTunnelPublicPathPrefix(mapping.PublicPathPrefix)
	if !ok {
		return "/"
	}
	return value
}

func frpMappingEntrypoint(mapping domain.FrpTunnelMapping) string {
	if mapping.Entrypoint == "web" || mapping.Entrypoint == "websecure" {
		return mapping.Entrypoint
	}
	if frpMappingPublicScheme(mapping) == "https" {
		return "websecure"
	}
	return "web"
}

func frpMappingPublicScheme(mapping domain.FrpTunnelMapping) string {
	if mapping.PublicScheme == "https" || mapping.Entrypoint == "websecure" {
		return "https"
	}
	return "http"
}
