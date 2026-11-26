package application

import (
	"context"
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func jenkinsPurposeToken(token, purpose string) string {
	return domain.HashSecret(token + ":" + purpose)
}

// 可信部署材料只经 SSH stdin 交给受管 broker，不进入项目参数、API 普通响应或日志。
func (r *Runtime) trustedJenkinsMaterials(ctx context.Context, state domain.State, plan domain.Object) (domain.Object, error) {
	snapshot := objectMapFromAny(plan["buildSnapshot"])
	source := cloneObject(objectMapFromAny(snapshot["sourceSnapshot"]))
	build := findObject(state.Servers, releasePlanBuildServerID(plan))
	managerID := stringValue(plan["deploymentManagerServerId"])
	manager := findObject(state.Servers, managerID)
	if build == nil || manager == nil {
		return nil, errors.New("JENKINS_DEPLOYMENT_SERVER_REQUIRED")
	}
	if jenkinsRuntimeBundleRevision(build) < 12 {
		return nil, errors.New("JENKINS_APPLICATION_BUNDLE_UPDATE_REQUIRED")
	}
	if stringValue(source["registryMirrorUrl"]) != "" && jenkinsRuntimeBundleRevision(build) < 13 {
		return nil, errors.New("JENKINS_MIRROR_BUNDLE_REQUIRED")
	}
	cluster := stringValue(plan["deploymentClusterId"])
	if cluster == "" || cluster != stringValue(serverRuntimeMetrics(manager)["swarmClusterId"]) {
		return nil, errors.New("APPLICATION_SWARM_CLUSTER_MISMATCH")
	}
	if err := validateDeploymentLocation(findObject(state.Applications, stringValue(plan["applicationId"])), plan, cluster, false); err != nil {
		return nil, err
	}
	token, err := r.readGitSourceToken(ctx, state, source)
	if err != nil {
		return nil, err
	}
	delete(source, "gitToken")
	delete(source, "sshPrivateKey")
	if token != "" {
		source["gitToken"] = token
	}
	delete(source, "authSecretRef")
	swarm := objectMapFromAny(snapshot["swarmRuntime"])
	if err := validateContainerRuntime(swarm); err != nil {
		return nil, err
	}
	if err := validateVolumeClaims(state, stringValue(plan["applicationId"]), swarm); err != nil {
		return nil, err
	}
	targets := []domain.Object{}
	servers := map[string]bool{managerID: true}
	services := []domain.Object{}
	snapshotValues := objectSlice(plan["configSnapshot"])
	buildValues, buildSecrets, err := r.jenkinsBuildInputs(ctx, snapshotValues, "")
	if err != nil {
		return nil, err
	}
	for _, configured := range objectSlice(swarm["services"]) {
		serverID := stringValue(configured["serverId"])
		server := findObject(state.Servers, serverID)
		nodeID := stringValue(serverRuntimeMetrics(server)["swarmNodeId"])
		if server == nil || nodeID == "" {
			return nil, errors.New("JENKINS_DEPLOYMENT_NODE_REQUIRED")
		}
		servers[serverID] = true
		environment, err := r.resolvedStageValues(ctx, snapshotValues, "runtime", stringValue(configured["slotKey"]))
		if err != nil {
			return nil, err
		}
		slotBuildValues, slotBuildSecrets, err := r.jenkinsBuildInputs(ctx, snapshotValues, stringValue(configured["slotKey"]))
		if err != nil {
			return nil, err
		}
		published := int(numberValue(configured["publishedPort"]))
		if published == 0 {
			published = int(numberValue(swarm["publishedPort"]))
		}
		serviceEnv := domain.Object{}
		for k, v := range environment {
			serviceEnv[k] = v
		}
		platform := dockerTargetPlatform(server)
		if frozen, ok := snapshot["slotPlatforms"]; ok && objectMapFromAny(frozen)[stringValue(configured["slotKey"])] != platform {
			return nil, errors.New("JENKINS_IMAGE_PLATFORM_MISMATCH")
		}
		services = append(services, domain.Object{"slotKey": configured["slotKey"], "serviceName": configured["serviceName"], "serverId": serverID, "nodeId": nodeID, "replicas": max(1, int(numberValue(configured["replicas"]))), "publishedPort": published, "ports": configuredServicePorts(swarm, configured), "platform": platform, "environment": serviceEnv, "buildValues": slotBuildValues, "buildSecrets": slotBuildSecrets})
	}
	for id := range servers {
		server := findObject(state.Servers, id)
		key, ok := r.serverSSHSecretMaterial(ctx, state, server)
		if !ok {
			return nil, errors.New("JENKINS_DEPLOYMENT_SSH_REQUIRED")
		}
		targets = append(targets, domain.Object{"id": id, "host": serverPublicHost(server), "port": numberOrDefault(server["port"], 22), "user": firstNonEmptyString(stringValue(server["sshUser"]), "root"), "key": key})
	}
	frozen := objectMapFromAny(snapshot["nodePullMirrors"])
	pull := objectMapFromAny(frozen[releasePlanBuildServerID(plan)])
	if len(pull) == 0 {
		pull, err = resolvePullMirrors(source, build)
		if err != nil {
			return nil, err
		}
	}
	gatewayPull := objectMapFromAny(frozen[managerID])
	if (pull["origin"] == "server" || gatewayPull["origin"] == "server") && jenkinsRuntimeBundleRevision(build) < 13 {
		return nil, errors.New("JENKINS_MIRROR_BUNDLE_REQUIRED")
	}
	registry, err := r.registryPushMaterials(ctx, state, plan)
	if err != nil {
		return nil, err
	}
	if registry != nil && jenkinsRuntimeBundleRevision(build) < 13 {
		return nil, errors.New("JENKINS_REGISTRY_SYNC_BUNDLE_REQUIRED")
	}
	sensitiveValues := []string{}
	for _, v := range snapshotValues {
		if v["valueSource"] == "secret_ref" {
			material, err := r.readApplicationMaterial(ctx, state, v)
			if err != nil {
				return nil, err
			}
			sensitiveValues = append(sensitiveValues, material)
		}
	}
	return domain.Object{"registrySync": registry, "sensitiveValues": sensitiveValues, "volumes": swarm["volumes"], "command": swarm["command"], "shellEntrypoint": swarm["shellEntrypoint"], "initializeCommand": swarm["initializeCommand"], "healthCommand": swarm["healthCommand"], "buildOutputKeys": plan["buildOutputKeys"], "buildValues": buildValues, "buildSecrets": buildSecrets, "version": 1, "applicationId": plan["applicationId"], "releasePlanId": plan["id"], "source": source, "buildServerId": plan["buildServerId"], "buildHost": serverPublicHost(build), "registryMirrors": pull["mirrors"], "serverRegistryMirrors": pull["trustedMirrors"], "buildPull": pull, "gatewayPull": gatewayPull, "deploymentManagerServerId": managerID, "clusterId": cluster, "network": firstNonEmptyString(stringValue(swarm["overlayNetwork"]), "iquops-public"), "containerPort": int(numberOrDefault(swarm["containerPort"], 80)), "services": services, "targets": targets}, nil
}

// 槽位覆盖可以改变同名输入的保密性，两张材料表必须采用相同的优先顺序。
func (r *Runtime) jenkinsBuildInputs(ctx context.Context, snapshot []domain.Object, slot string) (map[string]string, map[string]string, error) {
	public, err := r.resolvedStageValues(ctx, snapshot, "build", slot)
	if err != nil {
		return nil, nil, err
	}
	sensitivity := map[string]bool{}
	for _, scope := range []string{"global", "service"} {
		for _, v := range snapshot {
			if v["stage"] != "build" || firstNonEmptyString(stringValue(v["scope"]), "global") != scope || scope == "service" && (slot == "" || v["serviceSlotKey"] != slot) {
				continue
			}
			sensitivity[stringValue(v["key"])] = v["valueSource"] == "secret_ref"
		}
	}
	secrets := map[string]string{}
	for key, private := range sensitivity {
		if private {
			secrets[key] = public[key]
			delete(public, key)
		}
	}
	return public, secrets, nil
}
