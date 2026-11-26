package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type SystemRouteRuntimeExecutor struct{}

func NewSystemRouteRuntimeExecutor() *SystemRouteRuntimeExecutor {
	return &SystemRouteRuntimeExecutor{}
}

func (e *SystemRouteRuntimeExecutor) Apply(ctx context.Context, input RouteRuntimeInput) (domain.Object, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	client, err := sshClient(ctx, input.Gateway, input.SSHKey)
	if err != nil {
		return nil, errors.New("ROUTE_SSH_UNAVAILABLE")
	}
	defer func() { _ = client.Close() }()
	if input.LegacyRedirectHost != "" {
		return retireRemoteLegacyRedirect(ctx, client, input)
	}
	var content []byte
	if len(input.Redirect) > 0 {
		if err = inspectRouteGateway(ctx, client, input); err == nil {
			content, err = buildDomainRedirectRoute(input)
		}
	} else {
		var backends []domain.Object
		if backends, err = inspectRouteBackends(ctx, client, input); err == nil {
			content, err = buildTraefikRoute(input, backends)
		}
	}
	if err != nil {
		return nil, err
	}
	path, staged, backup := routeRemotePaths(stringValue(input.Route["id"]))
	input.Log("traefik", "route-apply", "info", "正在原子替换当前路由配置")
	if err := writeRemoteFile(ctx, client, staged, string(content)); err != nil {
		return nil, errors.New("ROUTE_CONFIG_UPLOAD_FAILED")
	}
	command := "set -eu; mkdir -p " + shellQuote(traefikRemoteRoot+"/route-backups") + "; if [ -f " + shellQuote(path) + " ]; then cp " + shellQuote(path) + " " + shellQuote(backup) + "; else rm -f " + shellQuote(backup) + "; fi; mv " + shellQuote(staged) + " " + shellQuote(path)
	if err := remoteRun(ctx, client, command); err != nil {
		return routeRuntimeSnapshot(input), errors.New("ROUTE_CONFIG_REPLACE_FAILED")
	}
	snapshot := routeRuntimeSnapshot(input)
	verifications, err := verifyRouteAccess(ctx, client, input, false)
	if err != nil {
		input.Log("traefik", "route-verify", "error", "访问校验失败，正在恢复上次路由配置")
		rollback := "if [ -f " + shellQuote(backup) + " ]; then mv " + shellQuote(backup) + " " + shellQuote(path) + "; else rm -f " + shellQuote(path) + "; fi"
		rollbackCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if rollbackErr := remoteRun(rollbackCtx, client, rollback); rollbackErr != nil {
			return snapshot, errors.New("ROUTE_ROLLBACK_FAILED")
		}
		return nil, err
	}
	snapshot["verifications"], snapshot["lastVerifiedAt"] = verifications, domain.Now()
	_ = remoteRun(ctx, client, "rm -f "+shellQuote(backup))
	return snapshot, nil
}

func (e *SystemRouteRuntimeExecutor) Remove(ctx context.Context, input RouteRuntimeInput) (domain.Object, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	client, err := sshClient(ctx, input.Gateway, input.SSHKey)
	if err != nil {
		return nil, errors.New("ROUTE_SSH_UNAVAILABLE")
	}
	defer func() { _ = client.Close() }()
	if err := inspectRouteGateway(ctx, client, input); err != nil {
		if errors.Is(err, errRouteClusterRecreated) {
			if cleanupErr := remoteRun(ctx, client, staleRouteCleanupCommand(stringValue(input.Route["id"]))); cleanupErr != nil {
				return nil, errors.New("ROUTE_STALE_CONFIG_REMOVE_FAILED")
			}
			return routeRuntimeSnapshot(input), nil
		}
		return nil, err
	}
	path, _, backup := routeRemotePaths(stringValue(input.Route["id"]))
	input.Log("traefik", "route-remove", "info", "正在从网关撤销当前路由")
	command := "mkdir -p " + shellQuote(traefikRemoteRoot+"/route-backups") + "; if [ -f " + shellQuote(path) + " ]; then mv " + shellQuote(path) + " " + shellQuote(backup) + "; fi"
	if err := remoteRun(ctx, client, command); err != nil {
		return routeRuntimeSnapshot(input), errors.New("ROUTE_CONFIG_REMOVE_FAILED")
	}
	if _, err := verifyRouteAccess(ctx, client, input, true); err != nil {
		rollbackCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if rollbackErr := remoteRun(rollbackCtx, client, "if [ -f "+shellQuote(backup)+" ]; then mv "+shellQuote(backup)+" "+shellQuote(path)+"; fi"); rollbackErr != nil {
			return routeRuntimeSnapshot(input), errors.New("ROUTE_ROLLBACK_FAILED")
		}
		return nil, err
	}
	_ = remoteRun(ctx, client, "rm -f "+shellQuote(backup))
	return routeRuntimeSnapshot(input), nil
}

func routeRuntimeSnapshot(input RouteRuntimeInput) domain.Object {
	snapshot := domain.Object{"gatewayServerId": input.Gateway["id"], "revision": input.Revision, "domains": input.Domains, "services": input.Services, "rules": input.Rules, "network": input.Network, "clusterId": input.ClusterID, "httpPort": input.HTTPPort, "httpsPort": input.HTTPSPort, "accessibleUrls": appliedRouteURLs(input)}
	if len(input.Redirect) > 0 {
		snapshot["redirect"] = input.Redirect
	}
	return snapshot
}

func appliedRouteURLs(input RouteRuntimeInput) []string {
	result := []string{}
	for _, asset := range input.Domains {
		scheme, port := "http", input.HTTPPort
		if asset["tls"] == true {
			scheme, port = "https", input.HTTPSPort
		}
		host := stringValue(asset["name"])
		if port > 0 && (scheme == "http" && port != 80 || scheme == "https" && port != 443) {
			host = net.JoinHostPort(host, strconv.Itoa(port))
		}
		for _, rule := range input.Rules {
			result = append(result, (&url.URL{Scheme: scheme, Host: host, Path: stringValue(rule["path"])}).String())
		}
	}
	return result
}

func routeRemotePaths(id string) (string, string, string) {
	name := "route-" + routeRevision(id)
	return traefikRemoteRoot + "/dynamic/" + name + ".yml", traefikRemoteRoot + "/dynamic/" + name + ".staged", traefikRemoteRoot + "/route-backups/" + name + ".previous"
}

func inspectRouteGateway(ctx context.Context, client *ssh.Client, input RouteRuntimeInput) error {
	status, err := readRemoteSwarmStatus(ctx, client, input.Network)
	if err != nil || !status.Active || status.Role != "manager" {
		return errors.New("ROUTE_SWARM_MANAGER_REQUIRED")
	}
	gateway, err := routeInspectService(ctx, client, input.TraefikName)
	if err != nil {
		return errors.New("ROUTE_TRAEFIK_UNAVAILABLE")
	}
	labels := objectMapFromAny(objectMapFromAny(gateway["Spec"])["Labels"])
	if stringValue(labels["iquops.owner"]) != "iquops" || stringValue(labels["iquops.component"]) != "traefik_gateway" {
		return errors.New("ROUTE_TRAEFIK_UNMANAGED")
	}
	if input.ClusterID != "" {
		cluster, err := remoteOutput(ctx, client, "docker info --format '{{.Swarm.Cluster.ID}}'")
		if err != nil {
			return errors.New("ROUTE_SWARM_PROBE_FAILED")
		}
		if cluster != input.ClusterID {
			return errRouteClusterRecreated
		}
	}
	return nil
}

var errRouteClusterRecreated = errors.New("ROUTE_SWARM_CLUSTER_RECREATED")

func staleRouteCleanupCommand(routeID string) string {
	path, staged, backup := routeRemotePaths(routeID)
	return "rm -f " + shellQuote(path) + " " + shellQuote(staged) + " " + shellQuote(backup)
}

func inspectRouteBackends(ctx context.Context, client *ssh.Client, input RouteRuntimeInput) ([]domain.Object, error) {
	if err := inspectRouteGateway(ctx, client, input); err != nil {
		return nil, err
	}
	networkOutput, err := remoteOutput(ctx, client, "docker network inspect "+shellQuote(input.Network)+" --format '{{.Id}}|{{.Driver}}|{{.Scope}}'")
	parts := strings.Split(networkOutput, "|")
	if err != nil || len(parts) != 3 || parts[1] != "overlay" || parts[2] != "swarm" {
		return nil, errors.New("ROUTE_OVERLAY_NETWORK_REQUIRED")
	}
	gateway, err := routeInspectService(ctx, client, input.TraefikName)
	if err != nil || !routeServiceOnNetwork(gateway, parts[0]) {
		return nil, errors.New("ROUTE_GATEWAY_NETWORK_MISMATCH")
	}
	result := []domain.Object{}
	for _, configured := range input.Services {
		weight, err := validatedRouteServiceWeight(configured)
		if err != nil {
			return nil, err
		}
		if weight == 0 {
			continue
		}
		name := stringValue(configured["serviceName"])
		service, err := routeInspectService(ctx, client, name)
		if err != nil {
			return nil, errors.New("ROUTE_SERVICE_NOT_DEPLOYED")
		}
		spec := objectMapFromAny(service["Spec"])
		labels := objectMapFromAny(spec["Labels"])
		if stringValue(labels["iquops.owner"]) != "iquops" || stringValue(labels["iquops.application-id"]) != input.ApplicationID || stringValue(labels["iquops.service-slot"]) != stringValue(configured["slotKey"]) {
			return nil, errors.New("ROUTE_SERVICE_OWNERSHIP_MISMATCH")
		}
		if !routeServiceOnNetwork(service, parts[0]) {
			return nil, errors.New("ROUTE_SERVICE_NETWORK_MISMATCH")
		}
		port, _ := strconv.Atoi(stringValue(labels["traefik.http.services."+safeName(name)+".loadbalancer.server.port"]))
		if pname := stringValue(configured["portName"]); pname != "" {
			scheme, raw, _ := strings.Cut(stringValue(labels["iquops.port."+pname]), "/")
			if scheme != "http" {
				return nil, errors.New("ROUTE_HTTP_PORT_UNAVAILABLE")
			}
			port, _ = strconv.Atoi(raw)
		} else if port == 0 {
			ports := objectSlice(objectMapFromAny(spec["EndpointSpec"])["Ports"])
			if len(ports) == 1 {
				port = int(numberValue(ports[0]["TargetPort"]))
			}
		}
		if port < 1 || port > 65535 {
			return nil, errors.New("ROUTE_SERVICE_PORT_AMBIGUOUS")
		}
		result = append(result, domain.Object{"name": name, "port": port, "weight": weight, "slotKey": configured["slotKey"]})
	}
	if len(result) == 0 {
		return nil, errors.New("ROUTE_SERVICE_WEIGHT_REQUIRED")
	}
	return result, nil
}

func routeInspectService(ctx context.Context, client *ssh.Client, name string) (domain.Object, error) {
	output, err := remoteOutput(ctx, client, "docker service inspect "+shellQuote(name))
	if err != nil {
		return nil, err
	}
	var items []domain.Object
	if err := json.Unmarshal([]byte(output), &items); err != nil || len(items) != 1 {
		return nil, errors.New("ROUTE_SERVICE_INSPECT_INVALID")
	}
	return items[0], nil
}

func routeServiceOnNetwork(service domain.Object, networkID string) bool {
	template := objectMapFromAny(objectMapFromAny(service["Spec"])["TaskTemplate"])
	for _, network := range objectSlice(template["Networks"]) {
		if stringValue(network["Target"]) == networkID {
			return true
		}
	}
	return false
}

func buildTraefikRoute(input RouteRuntimeInput, backends []domain.Object) ([]byte, error) {
	prefix := "iquops-" + routeRevision(input.Route["id"])
	services, routers, middlewares := domain.Object{}, domain.Object{}, domain.Object{}
	weighted := []domain.Object{}
	for index, backend := range backends {
		name := fmt.Sprintf("%s-service-%d", prefix, index)
		backendURL := stringValue(backend["url"])
		if backendURL == "" {
			backendURL = "http://" + stringValue(backend["name"]) + ":" + strconv.Itoa(int(numberValue(backend["port"])))
		}
		services[name] = domain.Object{"loadBalancer": domain.Object{"servers": []domain.Object{{"url": backendURL}}}}
		weighted = append(weighted, domain.Object{"name": name, "weight": backend["weight"]})
	}
	services[prefix] = domain.Object{"weighted": domain.Object{"services": weighted}}
	marker := prefix + "-revision"
	middlewares[marker] = domain.Object{"headers": domain.Object{"customResponseHeaders": domain.Object{"X-IQuOps-Route": routeRevision(input.Route["id"]) + ":" + input.Revision}}}
	for domainIndex, asset := range input.Domains {
		host := stringValue(asset["name"])
		for ruleIndex, rule := range input.Rules {
			name := fmt.Sprintf("%s-d%d-r%d", prefix, domainIndex, ruleIndex)
			chain := []string{marker}
			path := stringValue(rule["path"])
			switch stringValue(rule["pathHandling"]) {
			case "strip_prefix":
				middlewares[name+"-path"] = domain.Object{"stripPrefix": domain.Object{"prefixes": []string{path}}}
				chain = append(chain, name+"-path")
			case "rewrite":
				middlewares[name+"-path"] = domain.Object{"replacePath": domain.Object{"path": rule["rewriteTargetPath"]}}
				chain = append(chain, name+"-path")
			}
			router := domain.Object{"rule": "Host(" + strconv.Quote(host) + ") && PathPrefix(" + strconv.Quote(path) + ")", "service": prefix, "entryPoints": []string{"web"}, "middlewares": chain}
			routers[name+"-http"] = router
			if asset["tls"] == true {
				secure := cloneObject(router)
				secure["entryPoints"], secure["tls"] = []string{"websecure"}, domain.Object{"certResolver": managedTraefikResolver(input.CertResolver)}
				routers[name+"-https"] = secure
				if asset["inheritedUpgrade"] == true {
					upgrade := domain.Object{"scheme": "https", "targetHost": host, "mode": "permanent"}
					if input.HTTPSPort > 0 && input.HTTPSPort != 443 {
						upgrade["targetPort"] = input.HTTPSPort
					}
					middlewares[name+"-https"] = redirectMiddleware(host, upgrade)
					router["middlewares"] = []string{marker, name + "-https"}
				}
			}
		}
	}
	return json.MarshalIndent(domain.Object{"http": domain.Object{"routers": routers, "services": services, "middlewares": middlewares}}, "", "  ")
}
