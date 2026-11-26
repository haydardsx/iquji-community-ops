package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const swarmRebuildOwnerLabel = "iquops.swarm-rebuild"

// 逻辑备份仅适用于没有业务服务、secret/config 和其他成员的受限集群。
type swarmRebuildManifest struct {
	Version     int                  `json:"version"`
	BackupID    string               `json:"backupId"`
	GatewayName string               `json:"gatewayName"`
	Local       swarmLocalConnection `json:"local"`
	Cluster     json.RawMessage      `json:"cluster"`
	Node        json.RawMessage      `json:"node"`
	Networks    []json.RawMessage    `json:"networks"`
	Gateway     json.RawMessage      `json:"gateway,omitempty"`
}

func readSwarmRebuildManifest(ctx context.Context, call swarmEngineRequest, input ServerRuntimeExecutionInput, local swarmLocalConnection) (swarmRebuildManifest, error) {
	m := swarmRebuildManifest{Version: 1, BackupID: input.SwarmRebuildID, GatewayName: firstNonEmptyString(input.TraefikService, "iquops-traefik"), Local: local}
	var err error
	if m.Cluster, err = call(ctx, http.MethodGet, "/swarm", nil); err != nil {
		return m, err
	}
	var cluster struct {
		ID   string
		Spec struct {
			EncryptionConfig struct{ AutoLockManagers bool }
			CAConfig         struct {
				ExternalCAs []json.RawMessage
				ForceRotate uint64
			}
		}
		RootRotationInProgress bool
	}
	if json.Unmarshal(m.Cluster, &cluster) != nil || cluster.ID == "" {
		return m, errors.New("SWARM_REBUILD_STATE_UNAVAILABLE")
	}
	if cluster.Spec.EncryptionConfig.AutoLockManagers {
		return m, swarmRebuildScopeError("AUTOLOCK_ENABLED")
	}
	if len(cluster.Spec.CAConfig.ExternalCAs) != 0 {
		return m, swarmRebuildScopeError("EXTERNAL_CA_CONFIG")
	}
	if cluster.Spec.CAConfig.ForceRotate != 0 || cluster.RootRotationInProgress {
		return m, swarmRebuildScopeError("CA_ROTATION_ACTIVE")
	}
	raw, err := call(ctx, http.MethodGet, "/nodes", nil)
	var nodes []json.RawMessage
	if err != nil || json.Unmarshal(raw, &nodes) != nil {
		return m, errors.New("SWARM_REBUILD_STATE_UNAVAILABLE")
	}
	if len(nodes) != 1 {
		return m, swarmRebuildScopeError("MANAGER_COUNT")
	}
	m.Node = nodes[0]
	node := rebuildObject(m.Node)
	if stringValue(node["ID"]) == "" || stringValue(objectMapFromAny(node["Spec"])["Role"]) != "manager" {
		return m, swarmRebuildScopeError("MANAGER_IDENTITY")
	}
	for _, resource := range []string{"secrets", "configs"} {
		raw, err := call(ctx, http.MethodGet, "/"+resource, nil)
		var items []json.RawMessage
		if err != nil || json.Unmarshal(raw, &items) != nil {
			return m, errors.New("SWARM_REBUILD_STATE_UNAVAILABLE")
		}
		if len(items) != 0 {
			if resource == "secrets" {
				return m, swarmRebuildScopeError("SECRETS_PRESENT")
			}
			return m, swarmRebuildScopeError("CONFIGS_PRESENT")
		}
	}
	raw, err = call(ctx, http.MethodGet, "/services", nil)
	var services []json.RawMessage
	if err != nil || json.Unmarshal(raw, &services) != nil {
		return m, errors.New("SWARM_REBUILD_STATE_UNAVAILABLE")
	}
	if len(services) > 1 {
		return m, swarmRebuildScopeError("SERVICE_COUNT")
	}
	if len(services) == 1 {
		if objectMapFromAny(node["Spec"])["Availability"] != "active" {
			return m, swarmRebuildScopeError("NODE_AVAILABILITY")
		}
		m.Gateway = services[0]
		if err := validateSwarmRebuildGateway(m.Gateway, input, stringValue(node["ID"])); err != nil {
			return m, err
		}
		container := objectMapFromAny(objectMapFromAny(objectMapFromAny(rebuildObject(m.Gateway)["Spec"])["TaskTemplate"])["ContainerSpec"])
		image := stringValue(container["Image"])
		if !strings.Contains(image, "@sha256:") && !strings.HasPrefix(image, "sha256:") {
			return m, swarmRebuildScopeError("IMAGE_NOT_PINNED")
		}
		if _, err := call(ctx, http.MethodGet, "/images/"+url.PathEscape(image)+"/json", nil); err != nil {
			return m, swarmRebuildScopeError("IMAGE_CACHE_UNAVAILABLE")
		}
	}
	raw, err = call(ctx, http.MethodGet, "/networks?filters=%7B%22scope%22%3A%5B%22swarm%22%5D%7D", nil)
	var networks []struct{ ID string }
	if err != nil || json.Unmarshal(raw, &networks) != nil {
		return m, errors.New("SWARM_REBUILD_STATE_UNAVAILABLE")
	}
	for _, network := range networks {
		raw, err := call(ctx, http.MethodGet, "/networks/"+network.ID+"?verbose=true", nil)
		if err != nil {
			return m, err
		}
		if _, err := swarmRebuildNetworkBody(raw); err != nil {
			return m, err
		}
		if rebuildObject(raw)["Ingress"] == true && !swarmRebuildDefaultIngressMatches(m.Cluster, raw) {
			return m, swarmRebuildScopeError("NETWORK_INGRESS_UNSUPPORTED")
		}
		m.Networks = append(m.Networks, raw)
	}
	if len(m.Gateway) > 0 {
		mapping := map[string]string{}
		for _, raw := range m.Networks {
			network := rebuildObject(raw)
			id := stringValue(network["Id"])
			mapping[id], mapping[stringValue(network["Name"])] = id, id
		}
		if _, err := swarmRebuildGatewaySpec(m, stringValue(node["ID"]), mapping); err != nil {
			if err.Error() == "SWARM_REBUILD_SCOPE_UNSUPPORTED" {
				return m, swarmRebuildScopeError("GATEWAY_NETWORKS")
			}
			return m, err
		}
	}
	if err := validateSwarmRebuildContainers(ctx, call, m); err != nil {
		return m, err
	}
	return m, nil
}

func validateSwarmRebuildGateway(raw json.RawMessage, input ServerRuntimeExecutionInput, nodeID string) error {
	service := rebuildObject(raw)
	spec := objectMapFromAny(service["Spec"])
	labels := objectMapFromAny(spec["Labels"])
	name := firstNonEmptyString(input.TraefikService, "iquops-traefik")
	if stringValue(service["ID"]) == "" || stringValue(spec["Name"]) != name || labels["iquops.owner"] != "iquops" || labels["iquops.component"] != "traefik_gateway" || labels["iquops.lifecycle"] != "managed" {
		return swarmRebuildScopeError("GATEWAY_IDENTITY")
	}
	mode := objectMapFromAny(spec["Mode"])
	if len(mode) != 1 || domain.NumberValue(objectMapFromAny(mode["Replicated"])["Replicas"]) != 1 {
		return swarmRebuildScopeError("GATEWAY_MODE")
	}
	template := objectMapFromAny(spec["TaskTemplate"])
	container := objectMapFromAny(template["ContainerSpec"])
	if len(objectStringSlice(container["Command"])) > 0 || len(objectStringSlice(container["Env"])) > 0 {
		return swarmRebuildScopeError("GATEWAY_CUSTOM_COMMAND_OR_ENV")
	}
	if len(objectSlice(container["Secrets"])) != 0 {
		return swarmRebuildScopeError("GATEWAY_SECRET_REFERENCE")
	}
	if len(objectSlice(container["Configs"])) != 0 {
		return swarmRebuildScopeError("GATEWAY_CONFIG_REFERENCE")
	}
	requiredMounts := map[string]string{"/var/run/docker.sock": "/var/run/docker.sock", traefikRemoteRoot + "/dynamic": traefikRemoteRoot + "/dynamic", traefikRemoteRoot + "/certificates": "/certificates", traefikRemoteRoot + "/acme": traefikRemoteRoot + "/acme"}
	for _, mount := range objectSlice(container["Mounts"]) {
		source := stringValue(mount["Source"])
		if mount["Type"] != "bind" || path.Clean(source) != source || stringValue(mount["Target"]) == "" {
			return swarmRebuildScopeError("GATEWAY_MOUNTS")
		}
		if target, ok := requiredMounts[source]; ok && target == stringValue(mount["Target"]) && (source != "/var/run/docker.sock" || mount["ReadOnly"] == true) {
			delete(requiredMounts, source)
		}
		if source == "/var/run/docker.sock" && mount["ReadOnly"] == true && mount["Target"] == "/var/run/docker.sock" {
			continue
		}
		if !strings.HasPrefix(source, traefikRemoteRoot+"/") {
			return swarmRebuildScopeError("GATEWAY_MOUNTS")
		}
	}
	if len(requiredMounts) != 0 {
		return swarmRebuildScopeError("GATEWAY_REQUIRED_MOUNTS_MISSING")
	}
	storageReady, filesReady := false, false
	for _, argument := range objectStringSlice(container["Args"]) {
		lower := strings.ToLower(argument)
		if strings.HasPrefix(lower, "--configfile") || strings.HasPrefix(lower, "--providers.file.filename") {
			return swarmRebuildScopeError("GATEWAY_UNBACKED_CONFIG")
		}
		if strings.Contains(lower, ".acme.storage") {
			parts := strings.SplitN(argument, "=", 2)
			if len(parts) != 2 || path.Clean(parts[1]) != parts[1] || !strings.HasPrefix(parts[1], traefikRemoteRoot+"/acme/") {
				return swarmRebuildScopeError("GATEWAY_UNBACKED_ACME_STORAGE")
			}
			// 重建重放已安装网关的原始参数，校验其实际签发器和持久化路径。
			flags := strings.Split(strings.ToLower(parts[0]), ".")
			if len(flags) == 4 && flags[0] == "--certificatesresolvers" && flags[1] != "" &&
				flags[2] == "acme" && flags[3] == "storage" && parts[1] == traefikRemoteRoot+"/acme/acme.json" {
				storageReady = true
			}
		}
		if strings.HasPrefix(lower, "--providers.file.directory") {
			if argument != "--providers.file.directory="+traefikRemoteRoot+"/dynamic" {
				return swarmRebuildScopeError("GATEWAY_UNBACKED_CONFIG")
			}
			filesReady = true
		}
	}
	if !storageReady || !filesReady {
		return swarmRebuildScopeError("GATEWAY_PERSISTENT_PATHS_UNVERIFIED")
	}
	for _, constraint := range objectStringSlice(objectMapFromAny(template["Placement"])["Constraints"]) {
		compact := strings.ReplaceAll(constraint, " ", "")
		if strings.Contains(compact, "node.id") && compact != "node.id=="+nodeID {
			return swarmRebuildScopeError("GATEWAY_CONSTRAINTS")
		}
	}
	return nil
}

func validateSwarmRebuildContainers(ctx context.Context, call swarmEngineRequest, m swarmRebuildManifest) error {
	raw, err := call(ctx, http.MethodGet, "/containers/json?all=1", nil)
	var containers []struct {
		ID              string
		State           string
		Labels          map[string]string
		Mounts          []struct{ Source string }
		NetworkSettings struct {
			Networks map[string]struct{ NetworkID string }
		}
	}
	if err != nil || json.Unmarshal(raw, &containers) != nil {
		return errors.New("SWARM_REBUILD_STATE_UNAVAILABLE")
	}
	gatewayID := stringValue(rebuildObject(m.Gateway)["ID"])
	gatewayContainers, swarmNetworks := map[string]bool{}, map[string]bool{}
	for _, raw := range m.Networks {
		swarmNetworks[stringValue(rebuildObject(raw)["Id"])] = true
	}
	for _, container := range containers {
		if gatewayID != "" && container.Labels["com.docker.swarm.service.id"] == gatewayID {
			gatewayContainers[container.ID] = true
			continue
		}
		if (container.State == "exited" || container.State == "dead" || container.State == "created") && container.Labels["com.docker.swarm.service.name"] == m.GatewayName {
			gatewayContainers[container.ID] = true
			continue
		}
		for _, mount := range container.Mounts {
			if mount.Source == traefikRemoteRoot || strings.HasPrefix(mount.Source, traefikRemoteRoot+"/") {
				return swarmRebuildScopeError("SHARED_CONTAINER_DATA")
			}
		}
		for _, network := range container.NetworkSettings.Networks {
			if swarmNetworks[network.NetworkID] {
				return swarmRebuildScopeError("SHARED_CONTAINER_NETWORK")
			}
		}
	}
	for _, raw := range m.Networks {
		network := rebuildObject(raw)
		for containerID, endpoint := range objectMapFromAny(network["Containers"]) {
			if !gatewayContainers[containerID] && !swarmRebuildInternalNetworkEndpoint(network, containerID, objectMapFromAny(endpoint)) {
				return swarmRebuildScopeError("NETWORK_ENDPOINT")
			}
		}
	}
	return nil
}

// Moby 为 ingress 和普通 overlay 创建固定组合的负载均衡 sandbox，不属于用户容器。
func swarmRebuildInternalNetworkEndpoint(network domain.Object, containerID string, endpoint domain.Object) bool {
	name := stringValue(network["Name"])
	if network["Driver"] != "overlay" || network["Scope"] != "swarm" || name == "" || endpoint["Name"] != name+"-endpoint" {
		return false
	}
	if network["Ingress"] == true {
		return name == "ingress" && containerID == "ingress-sbox"
	}
	return containerID == "lb-"+name
}

func swarmRebuildNetworkBody(raw json.RawMessage) (domain.Object, error) {
	network := rebuildObject(raw)
	if stringValue(network["Id"]) == "" || stringValue(network["Name"]) == "" || network["ConfigOnly"] == true || stringValue(objectMapFromAny(network["ConfigFrom"])["Network"]) != "" {
		return nil, swarmRebuildScopeError("NETWORK_CONFIG")
	}
	if network["Driver"] != "overlay" || network["Scope"] != "swarm" {
		return nil, swarmRebuildScopeError("NETWORK_DRIVER")
	}
	originalIPAM := objectMapFromAny(network["IPAM"])
	ipam := domain.Object{}
	for _, key := range []string{"Driver", "Options", "Config"} {
		if value, ok := originalIPAM[key]; ok {
			ipam[key] = value
		}
	}
	if driver := stringValue(ipam["Driver"]); driver != "" && driver != "default" {
		return nil, swarmRebuildScopeError("NETWORK_IPAM")
	}
	options := objectMapFromAny(network["Options"])
	delete(options, "com.docker.network.driver.overlay.vxlanid_list")
	body := domain.Object{"Name": network["Name"], "Driver": "overlay", "Scope": "swarm", "CheckDuplicate": true, "IPAM": ipam, "Options": options}
	for _, key := range []string{"Internal", "Attachable", "Ingress", "EnableIPv6", "Labels"} {
		if value, ok := network[key]; ok {
			body[key] = value
		}
	}
	if len(objectMapFromAny(body["Labels"])) == 0 {
		delete(body, "Labels")
	}
	if len(objectMapFromAny(ipam["Options"])) == 0 {
		delete(ipam, "Options")
	}
	return body, nil
}

func rebuildObject(raw json.RawMessage) domain.Object {
	var result domain.Object
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	_ = decoder.Decode(&result)
	if result == nil {
		return domain.Object{}
	}
	return result
}
