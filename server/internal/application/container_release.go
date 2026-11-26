package application

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"golang.org/x/crypto/ssh"
)

func isContainerSource(source domain.Object) bool {
	switch stringValue(source["provider"]) {
	case "image", "apt", "dnf", "yum", "npm", "pypi":
		return true
	}
	return false
}

func containerBuildSnapshot(config domain.Object) domain.Object {
	return domain.Object{"executor": "swarm", "sourceSnapshot": cloneObject(objectMapFromAny(config["sourceConfig"])), "swarmRuntime": cloneObject(objectMapFromAny(config["swarmRuntime"]))}
}

// containerPeer 的秘密、构建参数与 Docker API 请求仅通过 SSH 标准输入发送。
type containerPeer struct {
	client *ssh.Client
	nodeID string
}

func (p containerPeer) call(ctx context.Context, cmd string, input []byte, code string) (string, error) {
	result, err := remoteOutputWithInput(ctx, p.client, cmd, input)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New(code)
	}
	return result, nil
}

func (r *Runtime) containerPeer(ctx context.Context, state domain.State, id string) (containerPeer, error) {
	server := findObject(state.Servers, id)
	key, ok := r.serverSSHSecretMaterial(ctx, state, server)
	if server == nil || !ok {
		return containerPeer{}, errors.New("SERVER_SSH_SECRET_MATERIAL_REQUIRED")
	}
	client, err := sshClient(ctx, server, key)
	if err != nil {
		return containerPeer{}, errors.New("APPLICATION_REMOTE_CONNECTION_FAILED")
	}
	return containerPeer{client: client, nodeID: stringValue(serverRuntimeMetrics(server)["swarmNodeId"])}, nil
}

func (r *Runtime) executeContainerRelease(ctx context.Context, task domain.Task) (string, error) {
	if task.TargetKind != "application" || task.TargetID == "" || stringValue(task.Payload["releasePlanId"]) == "" {
		return "", errors.New("APPLICATION_CONTAINER_RELEASE_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return "", err
	}
	plan := findObject(state.ReleasePlans, stringValue(task.Payload["releasePlanId"]))
	snapshot := objectMapFromAny(plan["buildSnapshot"])
	source := objectMapFromAny(snapshot["sourceSnapshot"])
	swarm := objectMapFromAny(snapshot["swarmRuntime"])
	if plan == nil || stringValue(plan["applicationId"]) != task.TargetID || !isContainerSource(source) {
		return "", errors.New("APPLICATION_CONTAINER_RELEASE_REQUIRED")
	}
	if _, err := validateContainerSource(source); err != nil {
		return "", err
	}
	if err := validateContainerRuntime(swarm); err != nil {
		return "", err
	}
	if err := validateVolumeClaims(state, task.TargetID, swarm); err != nil {
		return "", err
	}
	if err := validateDeploymentLocation(findObject(state.Applications, task.TargetID), plan, stringValue(plan["deploymentClusterId"]), false); err != nil {
		return "", err
	}
	if len(task.QueueResources) > 0 && !domain.TaskResourceScopeMatches(state, task) {
		return "", errors.New("TASK_RESOURCE_SCOPE_CHANGED")
	}
	inputs := objectSlice(plan["configSnapshot"])
	peers := map[string]containerPeer{}
	defer func() {
		for _, p := range peers {
			_ = p.client.Close()
		}
	}()
	managerID := stringValue(plan["deploymentManagerServerId"])
	nodes := []string{managerID}
	services := objectSlice(swarm["services"])
	if len(services) == 0 {
		return "", errors.New("SWARM_SERVICE_REQUIRED")
	}
	for _, s := range services {
		nodes = append(nodes, stringValue(s["serverId"]))
	}
	projectID := stringValue(plan["projectId"])
	for _, id := range nodes {
		if _, ok := peers[id]; ok {
			continue
		}
		server := findObject(state.Servers, id)
		if server == nil || !serverAvailableToProject(server, projectID) {
			return "", errors.New("APPLICATION_SERVER_NOT_AVAILABLE_TO_PROJECT")
		}
		p, e := r.containerPeer(ctx, state, id)
		if e != nil {
			return "", e
		}
		peers[id] = p
	}
	manager := peers[managerID]
	cluster := stringValue(plan["deploymentClusterId"])
	network := firstNonEmptyString(stringValue(swarm["overlayNetwork"]), "iquops-public")
	nid, err := manager.call(ctx, "test \"$(docker info --format '{{.Swarm.ControlAvailable}}|{{.Swarm.Cluster.ID}}')\" = "+shellQuote("true|"+cluster)+" && docker network inspect "+shellQuote(network)+" --format '{{if and (eq .Driver \"overlay\") (eq .Scope \"swarm\")}}{{.Id}}{{end}}'", nil, "APPLICATION_SWARM_CLUSTER_MISMATCH")
	if err != nil || cluster == "" || nid == "" {
		return "", errors.New("APPLICATION_SWARM_CLUSTER_MISMATCH")
	}
	r.appendRuntimeMessage(task, "application", "preflight", "info", "已核验 Swarm 管理节点与应用网络")
	existing := map[string]domain.Object{}
	for _, s := range services {
		id := stringValue(s["serverId"])
		peer := peers[id]
		name := stringValue(s["serviceName"])
		osID, e := peer.call(ctx, "docker info --format '{{.Swarm.NodeID}}'", nil, "APPLICATION_NODE_QUERY_FAILED")
		actual, e2 := manager.call(ctx, "docker node inspect "+shellQuote(peer.nodeID)+" --format '{{.Status.State}}|{{index .Spec.Labels \"iquops.server-id\"}}'", nil, "APPLICATION_NODE_QUERY_FAILED")
		if e != nil || e2 != nil || osID == "" || osID != peer.nodeID || actual != "ready|"+id {
			return "", errors.New("APPLICATION_NODE_OWNERSHIP_MISMATCH")
		}
		desc, e := inspectContainerService(ctx, manager, name, task.TargetID, stringValue(s["slotKey"]))
		if e != nil {
			return "", e
		}
		if e = checkVolumePlacement(desc, peer.nodeID); e != nil {
			return "", e
		}
		if err := ensureNodeVolumes(ctx, peer.call, task.TargetID, swarm, desc); err != nil {
			return "", err
		}
		existing[name] = desc
	}
	facts := make([]domain.Object, 0, len(services))
	released := objectSlice(plan["artifacts"])
	var pinned string
	if stringValue(source["provider"]) == "image" {
		pinned = stringValue(source["image"])
	} else {
		pinned = stringValue(source["baseImage"])
	}
	if len(released) > 0 {
		pinned = stringValue(released[0]["baseImageDigest"])
	}
	for _, s := range services {
		slot, name := stringValue(s["slotKey"]), stringValue(s["serviceName"])
		id := stringValue(s["serverId"])
		peer := peers[id]
		build, err := r.resolvedStageValues(ctx, inputs, "build", slot)
		if err != nil {
			return "", err
		}
		if stringValue(source["provider"]) == "image" && len(build) > 0 {
			return "", errors.New("APPLICATION_IMAGE_BUILD_INPUT_UNSUPPORTED")
		}
		env, err := r.resolvedStageValues(ctx, inputs, "runtime", slot)
		if err != nil {
			return "", err
		}
		r.appendRuntimeMessage(task, "application", "image", "info", "正在部署节点检查镜像与软件包版本")
		artifact := findObjectBy(released, "serviceSlotKey", slot)
		if artifact == nil {
			nodeSource := cloneObject(source)
			if frozen := objectMapFromAny(objectMapFromAny(snapshot["nodePullMirrors"])[id]); len(frozen) > 0 {
				nodeSource["registryMirrorUrl"] = frozen["pullMirror"]
				if frozen["verifyDaemon"] == true || frozen["origin"] == "daemon" {
					// HTTP 缓存需由目标 daemon 授权传输；还未应用的服务器草稿不能伪报成功。
					observed, probeErr := remoteDockerRegistryMirrors(ctx, peer.client)
					if probeErr == nil && frozen["verifyDaemon"] != true {
						if err := r.pinFirstDaemonMirrors(ctx, stringValue(plan["id"]), id, observed); err != nil {
							return "", err
						}
						frozen["mirrors"], frozen["verifyDaemon"] = observed, true
					}
					wanted := objectStringSlice(frozen["mirrors"])
					matched := slices.Equal(observed, wanted)
					if frozen["origin"] == "server" {
						matched = len(observed) > 0 && observed[0] == wanted[0]
					}
					if probeErr != nil || !matched {
						return "", errors.New("DOCKER_REGISTRY_MIRROR_DRIFT")
					}
				}
			}
			artifact, err = prepareContainerArtifact(ctx, peer, nodeSource, stringValue(plan["id"]), slot, pinned, build)
			if err != nil {
				return "", err
			}
			artifact["serviceSlotKey"], artifact["serverId"] = slot, id
		} else {
			actual, e := peer.call(ctx, "docker image inspect "+shellQuote(stringValue(artifact["image"]))+" --format '{{.Id}}'", nil, "APPLICATION_RELEASE_IMAGE_UNAVAILABLE")
			if e != nil || actual != artifact["imageDigest"] {
				return "", errors.New("APPLICATION_RELEASE_IMAGE_UNAVAILABLE")
			}
		}
		pinned = stringValue(artifact["baseImageDigest"])
		if err := checkImageShells(ctx, peer, stringValue(artifact["image"]), source, swarm); err != nil {
			return "", err
		}
		facts = append(facts, artifact)
		r.appendRuntimeMessage(task, "application", "deploy", "info", "正在应用 Swarm 服务配置")
		spec, err := containerServiceSpec(swarm, s, task.TargetID, nid, network, peer.nodeID, artifact, env, existing[name])
		if err != nil {
			return "", err
		}
		// 在服务变更前固定制品并校验当前 Worker 租约，重试沿用同一槽位的制品。
		err = r.Store.MutateState(ctx, func(st *domain.State) error {
			current := findObject(st.ReleasePlans, stringValue(plan["id"]))
			if current == nil {
				return errors.New("RELEASE_PLAN_NOT_FOUND")
			}
			if err := pinReleaseLocation(st, current); err != nil {
				return err
			}
			stored := objectSlice(current["artifacts"])
			if findObjectBy(stored, "serviceSlotKey", slot) == nil {
				current["artifacts"] = append(stored, artifact)
			}
			current["updatedAt"] = domain.Now()
			return nil
		})
		if err != nil {
			return "", err
		}
		releaseID := stringValue(plan["id"])
		prior := objectMapFromAny(existing[name]["Spec"])
		// Worker 在提交后中断时，沿用已生效的服务版本，避免重启同一发布的容器。
		if objectMapFromAny(prior["Labels"])["iquops.release-plan"] == releaseID {
			spec = prior
		} else {
			objectMapFromAny(spec["Labels"])["iquops.release-plan"] = releaseID
			if err := submitContainerService(ctx, manager, spec, existing[name]); err != nil {
				return "", err
			}
		}
		generation := int(numberValue(objectMapFromAny(spec["TaskTemplate"])["ForceUpdate"]))
		if err := verifyContainerRelease(ctx, manager, peer, s, swarm, stringValue(artifact["imageDigest"]), generation); err != nil {
			return "", err
		}
	}
	observed, err := r.ApplicationRuntime(ctx, task.TargetID)
	if err != nil || observed.Status != "running" {
		return "", errors.New("APPLICATION_RELEASE_READBACK_FAILED")
	}
	err = r.Store.MutateState(ctx, func(st *domain.State) error {
		current := findObject(st.ReleasePlans, stringValue(plan["id"]))
		if current == nil {
			return errors.New("RELEASE_PLAN_NOT_FOUND")
		}
		current["status"], current["updatedAt"], current["artifacts"] = "succeeded", domain.Now(), facts
		digests := []string{}
		for _, artifact := range facts {
			digest := stringValue(artifact["imageDigest"])
			if !containsString(digests, digest) {
				digests = append(digests, digest)
			}
		}
		current["imageDigest"] = strings.Join(digests, ",")
		recordAppliedApplicationInputs(st, task.TargetID, inputs, "build", "runtime")
		return nil
	})
	return "应用已在目标节点运行并通过验证", err
}

func inspectContainerService(ctx context.Context, p containerPeer, name, appID, slot string) (domain.Object, error) {
	names, err := p.call(ctx, "docker service ls --format '{{.Name}}'", nil, "APPLICATION_SERVICES_QUERY_FAILED")
	if err != nil {
		return nil, err
	}
	if !containsString(strings.Fields(names), name) {
		return nil, nil
	}
	raw, err := p.call(ctx, "docker service inspect "+shellQuote(name)+" --format '{{json .}}'", nil, "APPLICATION_SERVICES_QUERY_FAILED")
	var obj domain.Object
	if err != nil || json.Unmarshal([]byte(raw), &obj) != nil {
		return nil, errors.New("APPLICATION_SERVICES_QUERY_FAILED")
	}
	spec := objectMapFromAny(obj["Spec"])
	labels := objectMapFromAny(spec["Labels"])
	if labels["iquops.owner"] != "iquops" || labels["iquops.application-id"] != appID || labels["iquops.service-slot"] != slot || objectMapFromAny(spec["Mode"])["Replicated"] == nil {
		return nil, errors.New("APPLICATION_SERVICE_OWNERSHIP_MISMATCH")
	}
	return obj, nil
}
