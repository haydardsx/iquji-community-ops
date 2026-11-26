package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func swarmRebuildInitBody(m swarmRebuildManifest, target swarmLocalConnection) (domain.Object, error) {
	cluster, node := rebuildObject(m.Cluster), rebuildObject(m.Node)
	spec := swarmRebuildUserSpec(cluster["Spec"])
	labels := objectMapFromAny(spec["Labels"])
	labels[swarmRebuildOwnerLabel] = m.BackupID
	spec["Labels"] = labels
	pool := cluster["DefaultAddrPool"]
	if len(objectStringSlice(pool)) == 0 {
		pool = []string{"10.0.0.0/8"}
	}
	return domain.Object{"ListenAddr": target.ListenAddr, "AdvertiseAddr": target.AdvertiseAddr, "DataPathAddr": target.DataPathAddr, "ForceNewCluster": false, "Spec": spec, "AutoLockManagers": false, "DataPathPort": cluster["DataPathPort"], "DefaultAddrPool": pool, "SubnetSize": cluster["SubnetSize"], "Availability": objectMapFromAny(node["Spec"])["Availability"]}, nil
}

func swarmRebuildTarget(m swarmRebuildManifest, address string) (swarmLocalConnection, error) {
	ip := net.ParseIP(address)
	_, port, err := net.SplitHostPort(m.Local.AdvertiseAddr)
	if ip == nil || err != nil {
		return swarmLocalConnection{}, errors.New("SWARM_REBUILD_STATE_UNAVAILABLE")
	}
	return swarmLocalConnection{ListenAddr: m.Local.ListenAddr, AdvertiseAddr: net.JoinHostPort(ip.String(), port), DataPathAddr: ip.String()}, nil
}

func stopSwarmRebuildGateway(ctx context.Context, remote swarmRebuildRemote, m swarmRebuildManifest) error {
	service := rebuildObject(m.Gateway)
	name := stringValue(objectMapFromAny(service["Spec"])["Name"])
	raw, err := remote.Call(ctx, http.MethodGet, "/services", nil)
	var services []json.RawMessage
	if err != nil || json.Unmarshal(raw, &services) != nil {
		return errors.New("SWARM_REBUILD_STATE_UNAVAILABLE")
	}
	if len(m.Gateway) == 0 {
		if len(services) > 0 {
			return errors.New("SWARM_REBUILD_SCOPE_UNSUPPORTED")
		}
		return nil
	}
	for _, raw := range services {
		actual := rebuildObject(raw)
		if stringValue(actual["ID"]) != stringValue(service["ID"]) || swarmValueHash(actual["Spec"]) != swarmValueHash(service["Spec"]) {
			return errors.New("SWARM_REBUILD_SCOPE_UNSUPPORTED")
		}
		id := stringValue(actual["ID"])
		if _, err := remote.Call(ctx, http.MethodDelete, "/services/"+id, nil); err != nil {
			return err
		}
	}
	return waitSwarmRebuildGatewayStopped(ctx, remote, name)
}

func waitSwarmRebuildGatewayStopped(ctx context.Context, remote swarmRebuildRemote, name string) error {
	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for {
		filters, _ := json.Marshal(domain.Object{"label": []string{"com.docker.swarm.service.name=" + name}})
		raw, err := remote.Call(waitCtx, http.MethodGet, "/containers/json?filters="+url.QueryEscape(string(filters)), nil)
		var running []json.RawMessage
		if err == nil && json.Unmarshal(raw, &running) == nil && len(running) == 0 {
			return nil
		}
		if err := waitSwarmConnectionTick(waitCtx); err != nil {
			return errors.New("SWARM_REBUILD_VERIFY_FAILED")
		}
	}
}

func waitSwarmRebuildDetachedGateway(ctx context.Context, remote swarmRebuildRemote, m swarmRebuildManifest) error {
	if len(m.Gateway) == 0 {
		return nil
	}
	raw, err := remote.Call(ctx, http.MethodGet, "/services", nil)
	var services []json.RawMessage
	if err != nil || json.Unmarshal(raw, &services) != nil {
		return errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
	}
	if len(services) == 0 {
		return waitSwarmRebuildGatewayStopped(ctx, remote, m.GatewayName)
	}
	return nil
}

func waitSwarmRebuildOverlaysRemoved(ctx context.Context, remote swarmRebuildRemote) error {
	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for {
		raw, err := remote.Call(waitCtx, http.MethodGet, "/networks?filters=%7B%22scope%22%3A%5B%22swarm%22%5D%7D", nil)
		var networks []json.RawMessage
		if err == nil && json.Unmarshal(raw, &networks) == nil && len(networks) == 0 {
			return nil
		}
		if err := waitSwarmConnectionTick(waitCtx); err != nil {
			return errors.New("SWARM_REBUILD_VERIFY_FAILED")
		}
	}
}

func initSwarmRebuild(ctx context.Context, remote swarmRebuildRemote, m swarmRebuildManifest, target swarmLocalConnection) (remoteSwarmStatus, error) {
	body, err := swarmRebuildInitBody(m, target)
	if err != nil {
		return remoteSwarmStatus{}, err
	}
	if _, err = remote.Call(ctx, http.MethodPost, "/swarm/init", body); err != nil {
		return remoteSwarmStatus{}, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for {
		status, err := remote.Status(waitCtx)
		if err == nil && status.Active && status.ControlAvailable && status.Role == "manager" && status.NodeID != "" && status.ClusterID != "" && status.ManagerAddress == target.AdvertiseAddr {
			return status, nil
		}
		if err := waitSwarmConnectionTick(waitCtx); err != nil {
			return status, errors.New("SWARM_REBUILD_VERIFY_FAILED")
		}
	}
}

func restoreSwarmRebuildResources(ctx context.Context, remote swarmRebuildRemote, m swarmRebuildManifest, nodeID string) (domain.Object, error) {
	raw, err := remote.Call(ctx, http.MethodGet, "/nodes/"+nodeID, nil)
	if err != nil {
		return nil, err
	}
	node := rebuildObject(raw)
	version := fmt.Sprint(objectMapFromAny(node["Version"])["Index"])
	if _, err := remote.Call(ctx, http.MethodPost, "/nodes/"+nodeID+"/update?version="+url.QueryEscape(version), objectMapFromAny(rebuildObject(m.Node)["Spec"])); err != nil {
		return nil, err
	}
	mapping, err := restoreSwarmRebuildNetworks(ctx, remote, m)
	if err != nil {
		return nil, err
	}
	if len(m.Gateway) == 0 {
		return nil, nil
	}
	spec, err := swarmRebuildGatewaySpec(m, nodeID, mapping)
	if err != nil {
		return nil, err
	}
	if _, err := remote.Call(ctx, http.MethodPost, "/services/create", spec); err != nil {
		return nil, err
	}
	return spec, nil
}

func restoreSwarmRebuildNetworks(ctx context.Context, remote swarmRebuildRemote, m swarmRebuildManifest) (map[string]string, error) {
	raw, err := remote.Call(ctx, http.MethodGet, "/networks?filters=%7B%22scope%22%3A%5B%22swarm%22%5D%7D", nil)
	var existing []json.RawMessage
	if err != nil || json.Unmarshal(raw, &existing) != nil {
		return nil, errors.New("SWARM_REBUILD_STATE_UNAVAILABLE")
	}
	// fresh init 自动创建的 ingress 尚无服务，先移除再按原公开配置恢复。
	for _, raw := range existing {
		network := rebuildObject(raw)
		if network["Ingress"] != true {
			return nil, errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
		}
		if _, err := remote.Call(ctx, http.MethodDelete, "/networks/"+stringValue(network["Id"]), nil); err != nil {
			return nil, err
		}
	}
	if err := waitSwarmRebuildOverlaysRemoved(ctx, remote); err != nil {
		return nil, err
	}
	mapping := map[string]string{}
	for _, raw := range m.Networks {
		body, err := swarmRebuildNetworkBody(raw)
		if err != nil {
			return nil, err
		}
		response, err := remote.Call(ctx, http.MethodPost, "/networks/create", body)
		if err != nil {
			return nil, err
		}
		created := rebuildObject(response)
		id := firstNonEmptyString(stringValue(created["Id"]), stringValue(created["ID"]))
		if id == "" {
			return nil, errors.New("SWARM_REBUILD_VERIFY_FAILED")
		}
		original := rebuildObject(raw)
		mapping[stringValue(original["Id"])], mapping[stringValue(original["Name"])] = id, id
	}
	return mapping, nil
}

func swarmRebuildGatewaySpec(m swarmRebuildManifest, nodeID string, mapping map[string]string) (domain.Object, error) {
	spec := objectMapFromAny(rebuildObject(m.Gateway)["Spec"])
	template := objectMapFromAny(spec["TaskTemplate"])
	for _, holder := range []domain.Object{spec, template} {
		for _, network := range objectSlice(holder["Networks"]) {
			mapped := mapping[stringValue(network["Target"])]
			if mapped == "" {
				return nil, errors.New("SWARM_REBUILD_SCOPE_UNSUPPORTED")
			}
			network["Target"] = mapped
		}
	}
	oldNode := stringValue(rebuildObject(m.Node)["ID"])
	placement := objectMapFromAny(template["Placement"])
	constraints := objectStringSlice(placement["Constraints"])
	for i, constraint := range constraints {
		if strings.ReplaceAll(constraint, " ", "") == "node.id=="+oldNode {
			constraints[i] = "node.id==" + nodeID
		}
	}
	if len(constraints) > 0 {
		placement["Constraints"] = constraints
		template["Placement"] = placement
	}
	spec["TaskTemplate"] = template
	return spec, nil
}

func verifySwarmRebuild(ctx context.Context, remote swarmRebuildRemote, m swarmRebuildManifest, cp swarmRebuildCheckpoint, target swarmLocalConnection) error {
	status, err := remote.Status(ctx)
	if err != nil || !status.Active || !status.ControlAvailable {
		return swarmRebuildVerificationError("MANAGER_UNAVAILABLE")
	}
	if status.ManagerAddress != target.AdvertiseAddr {
		return swarmRebuildVerificationError("MANAGER_ENDPOINT_CHANGED")
	}
	if status.ClusterID != cp.NewClusterID || status.NodeID != cp.NewNodeID {
		return swarmRebuildVerificationError("CLUSTER_OR_NODE_ID_CHANGED")
	}
	local, err := remote.Local(ctx)
	if err != nil || local != target {
		return swarmRebuildVerificationError("LOCAL_CONNECTION_CHANGED")
	}
	rawCluster, err := remote.Call(ctx, http.MethodGet, "/swarm", nil)
	if err != nil {
		return swarmRebuildVerificationError("CLUSTER_UNAVAILABLE")
	}
	if !swarmRebuildClusterNetworkEqual(rebuildObject(m.Cluster), rebuildObject(rawCluster)) {
		return swarmRebuildVerificationError("CLUSTER_NETWORK_CONFIG_CHANGED")
	}
	actual, err := readSwarmRebuildManifest(ctx, remote.Call, ServerRuntimeExecutionInput{SwarmRebuildID: m.BackupID, TraefikService: stringValue(objectMapFromAny(rebuildObject(m.Gateway)["Spec"])["Name"])}, local)
	if err != nil {
		return err
	}
	wantSpec := swarmRebuildUserSpec(rebuildObject(m.Cluster)["Spec"])
	gotSpec := swarmRebuildUserSpec(rebuildObject(actual.Cluster)["Spec"])
	originalCluster, actualCluster := rebuildObject(m.Cluster), rebuildObject(actual.Cluster)
	if !swarmRebuildClusterNetworkEqual(originalCluster, actualCluster) {
		return swarmRebuildVerificationError("CLUSTER_NETWORK_CONFIG_CHANGED")
	}
	if swarmValueHash(wantSpec) != swarmValueHash(gotSpec) {
		return swarmRebuildVerificationError("CLUSTER_SPEC_CHANGED")
	}
	if swarmValueHash(objectMapFromAny(rebuildObject(m.Node)["Spec"])) != swarmValueHash(objectMapFromAny(rebuildObject(actual.Node)["Spec"])) {
		return swarmRebuildVerificationError("NODE_SPEC_CHANGED")
	}
	if len(actual.Networks) != len(m.Networks) || (len(actual.Gateway) == 0) != (len(m.Gateway) == 0) {
		return swarmRebuildVerificationError("RESOURCE_COUNT_CHANGED")
	}
	mapping := map[string]string{}
	for _, oldRaw := range m.Networks {
		old := rebuildObject(oldRaw)
		want, _ := swarmRebuildNetworkBody(oldRaw)
		found := false
		for _, newRaw := range actual.Networks {
			current := rebuildObject(newRaw)
			if current["Name"] != old["Name"] {
				continue
			}
			got, _ := swarmRebuildNetworkBody(newRaw)
			if swarmValueHash(want) != swarmValueHash(got) {
				return swarmRebuildVerificationError("NETWORK_CONFIG_CHANGED")
			}
			id := stringValue(current["Id"])
			mapping[stringValue(old["Id"])], mapping[stringValue(old["Name"])] = id, id
			found = true
		}
		if !found {
			return swarmRebuildVerificationError("NETWORK_MISSING")
		}
	}
	if len(m.Gateway) > 0 {
		want, err := swarmRebuildGatewaySpec(m, status.NodeID, mapping)
		if err != nil || swarmValueHash(want) != swarmValueHash(objectMapFromAny(rebuildObject(actual.Gateway)["Spec"])) {
			return swarmRebuildVerificationError("GATEWAY_SPEC_CHANGED")
		}
		raw, err := remote.Call(ctx, http.MethodGet, "/tasks?filters="+url.QueryEscape(`{"service":["`+stringValue(rebuildObject(actual.Gateway)["ID"])+`"]}`), nil)
		var tasks []struct {
			DesiredState string
			Status       struct{ State string }
		}
		if err != nil || json.Unmarshal(raw, &tasks) != nil {
			return swarmRebuildVerificationError("GATEWAY_TASKS_UNAVAILABLE")
		}
		ready := false
		for _, task := range tasks {
			if task.DesiredState == "running" && task.Status.State == "running" {
				ready = true
			}
		}
		if !ready {
			return swarmRebuildVerificationError("GATEWAY_NOT_RUNNING")
		}
	}
	return nil
}

func swarmRebuildClusterNetworkEqual(original, current domain.Object) bool {
	return domain.NumberValue(original["DataPathPort"]) == domain.NumberValue(current["DataPathPort"]) && domain.NumberValue(original["SubnetSize"]) == domain.NumberValue(current["SubnetSize"]) && swarmValueHash(effectiveSwarmDefaultAddrPool(objectStringSlice(original["DefaultAddrPool"]))) == swarmValueHash(effectiveSwarmDefaultAddrPool(objectStringSlice(current["DefaultAddrPool"])))
}

func clearSwarmRebuildOwner(ctx context.Context, remote swarmRebuildRemote) error {
	raw, err := remote.Call(ctx, http.MethodGet, "/swarm", nil)
	if err != nil {
		return err
	}
	cluster := rebuildObject(raw)
	spec := objectMapFromAny(cluster["Spec"])
	if stringValue(objectMapFromAny(spec["Labels"])[swarmRebuildOwnerLabel]) == "" {
		return nil
	}
	delete(objectMapFromAny(spec["Labels"]), swarmRebuildOwnerLabel)
	version := fmt.Sprint(objectMapFromAny(cluster["Version"])["Index"])
	_, err = remote.Call(ctx, http.MethodPost, "/swarm/update?version="+url.QueryEscape(version), spec)
	return err
}

func commitSwarmRebuild(ctx context.Context, input ServerRuntimeExecutionInput, remote swarmRebuildRemote, cp *swarmRebuildCheckpoint, phase string) error {
	previous := cp.Phase
	cp.Phase = phase
	if err := saveSwarmRebuildCheckpoint(ctx, input, *cp); err != nil {
		cp.Phase = previous
		return err
	}
	cleanupSwarmRebuildOwner(ctx, input, remote)
	return nil
}

func cleanupSwarmRebuildOwner(ctx context.Context, input ServerRuntimeExecutionInput, remote swarmRebuildRemote) {
	if err := clearSwarmRebuildOwner(ctx, remote); err != nil {
		input.LogStep("swarm", "rebuild-cleanup", "warn", "恢复结果已核验并提交，事务归属标记暂未清理；不回退已完成的集群")
	}
}

func swarmRebuildBaselineEqual(original, current swarmRebuildManifest) bool {
	oldCluster, newCluster := rebuildObject(original.Cluster), rebuildObject(current.Cluster)
	if oldCluster["ID"] != newCluster["ID"] || swarmValueHash(swarmRebuildUserSpec(oldCluster["Spec"])) != swarmValueHash(swarmRebuildUserSpec(newCluster["Spec"])) || stringValue(rebuildObject(original.Node)["ID"]) != stringValue(rebuildObject(current.Node)["ID"]) || swarmValueHash(rebuildObject(original.Node)["Spec"]) != swarmValueHash(rebuildObject(current.Node)["Spec"]) || len(original.Networks) != len(current.Networks) {
		return false
	}
	for _, key := range []string{"DataPathPort", "SubnetSize"} {
		if domain.NumberValue(oldCluster[key]) != domain.NumberValue(newCluster[key]) {
			return false
		}
	}
	if swarmValueHash(effectiveSwarmDefaultAddrPool(objectStringSlice(oldCluster["DefaultAddrPool"]))) != swarmValueHash(effectiveSwarmDefaultAddrPool(objectStringSlice(newCluster["DefaultAddrPool"]))) {
		return false
	}
	for _, oldRaw := range original.Networks {
		old := rebuildObject(oldRaw)
		found := false
		for _, newRaw := range current.Networks {
			if stringValue(rebuildObject(newRaw)["Id"]) != stringValue(old["Id"]) {
				continue
			}
			want, wantErr := swarmRebuildNetworkBody(oldRaw)
			got, gotErr := swarmRebuildNetworkBody(newRaw)
			if wantErr != nil || gotErr != nil || swarmValueHash(want) != swarmValueHash(got) {
				return false
			}
			found = true
		}
		if !found {
			return false
		}
	}
	return true
}

type swarmRebuildVerificationError string

func (swarmRebuildVerificationError) Error() string             { return "SWARM_REBUILD_VERIFY_FAILED" }
func (err swarmRebuildVerificationError) RebuildReason() string { return string(err) }

func swarmRebuildUserSpec(value any) domain.Object {
	raw, _ := json.Marshal(value)
	spec := rebuildObject(raw)
	labels := objectMapFromAny(spec["Labels"])
	delete(labels, swarmRebuildOwnerLabel)
	if len(labels) == 0 {
		delete(spec, "Labels")
	} else {
		spec["Labels"] = labels
	}
	return spec
}

func swarmRebuildOwnedNetworksSafe(original, current swarmRebuildManifest) bool {
	for _, newRaw := range current.Networks {
		newNetwork := rebuildObject(newRaw)
		found := false
		for _, oldRaw := range original.Networks {
			if rebuildObject(oldRaw)["Name"] != newNetwork["Name"] {
				continue
			}
			want, wantErr := swarmRebuildNetworkBody(oldRaw)
			got, gotErr := swarmRebuildNetworkBody(newRaw)
			if wantErr != nil || gotErr != nil || swarmValueHash(want) != swarmValueHash(got) {
				return false
			}
			found = true
		}
		if !found {
			return false
		}
	}
	return true
}

func swarmRebuildDefaultIngressMatches(clusterRaw, networkRaw json.RawMessage) bool {
	network := rebuildObject(networkRaw)
	if network["Name"] != "ingress" || network["Ingress"] != true || network["Attachable"] == true || network["Internal"] == true || network["EnableIPv6"] == true || len(objectMapFromAny(network["Labels"])) != 0 {
		return false
	}
	body, err := swarmRebuildNetworkBody(networkRaw)
	if err != nil || len(objectMapFromAny(body["Options"])) != 0 {
		return false
	}
	cluster := rebuildObject(clusterRaw)
	pool := effectiveSwarmDefaultAddrPool(objectStringSlice(cluster["DefaultAddrPool"]))
	prefix, err := netip.ParsePrefix(pool[0])
	bits := domain.NumberValue(cluster["SubnetSize"])
	if err != nil || !prefix.Addr().Is4() || bits < prefix.Bits() || bits > 30 {
		return false
	}
	ipam := objectMapFromAny(body["IPAM"])
	configs := objectSlice(ipam["Config"])
	if len(configs) != 1 || len(objectMapFromAny(ipam["Options"])) != 0 || len(objectMapFromAny(configs[0]["AuxiliaryAddresses"])) != 0 || stringValue(configs[0]["IPRange"]) != "" {
		return false
	}
	subnet := netip.PrefixFrom(prefix.Masked().Addr(), bits).Masked()
	return stringValue(configs[0]["Subnet"]) == subnet.String() && stringValue(configs[0]["Gateway"]) == subnet.Addr().Next().String()
}
