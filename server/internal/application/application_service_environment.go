package application

import (
	"context"
	"errors"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 修改实际服务的变量和副本，沿用发布执行器的归属检查、版本和 SSH 输入通道。
func (r *applicationSSHRemote) applyEnvironment(ctx context.Context, appID string, service ApplicationServiceState, values map[string]string, managed []string, replicas int) error {
	peer := containerPeer{client: r.client}
	existing, err := inspectContainerService(ctx, peer, service.ServiceName, appID, service.ServiceSlotKey)
	if err != nil {
		return err
	}
	if existing == nil || stringValue(existing["ID"]) != service.ServiceID {
		return errors.New("APPLICATION_SERVICE_OWNERSHIP_MISMATCH")
	}
	spec := objectMapFromAny(existing["Spec"])
	task := objectMapFromAny(spec["TaskTemplate"])
	container := objectMapFromAny(task["ContainerSpec"])
	mode := objectMapFromAny(objectMapFromAny(spec["Mode"])["Replicated"])
	if len(container) == 0 || len(mode) == 0 {
		return errors.New("APPLICATION_SERVICE_MODE_UNSUPPORTED")
	}
	remove := stringSet(managed)
	for k := range values {
		remove[k] = true
	}
	env := []string{}
	for _, pair := range objectStringSlice(container["Env"]) {
		key, _, _ := strings.Cut(pair, "=")
		if !remove[key] {
			env = append(env, pair)
		}
	}
	for _, key := range sortedKeysOfApplicationValues(values) {
		env = append(env, key+"="+values[key])
	}
	container["Env"] = env
	task["ForceUpdate"] = numberValue(task["ForceUpdate"]) + 1
	mode["Replicas"] = replicas
	return submitContainerService(ctx, peer, spec, existing)
}

// 初始化材料只能由同一套初始化和健康检查确认；启动成功并不能证明数据库账号已生效。
func (r *Runtime) verifyRestartedContainerInputs(ctx context.Context, config domain.Object, snapshot ApplicationRuntimeSnapshot, appID string) error {
	swarm := objectMapFromAny(config["swarmRuntime"])
	if len(swarm) == 0 || len(objectStringSlice(swarm["initializeCommand"]))+len(objectStringSlice(swarm["healthCommand"])) == 0 {
		return nil
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return err
	}
	manager, err := r.containerPeer(ctx, state, stringValue(config["deploymentManagerServerId"]))
	if err != nil {
		return err
	}
	defer manager.client.Close()
	cluster := snapshot.DeploymentClusterID
	actual, err := manager.call(ctx, "docker info --format '{{.Swarm.ControlAvailable}}|{{.Swarm.Cluster.ID}}'", nil, "APPLICATION_SWARM_CLUSTER_MISMATCH")
	if err != nil || cluster == "" || actual != "true|"+cluster {
		return errors.New("APPLICATION_SWARM_CLUSTER_MISMATCH")
	}
	for _, service := range snapshot.Services {
		if service.DesiredReplicas <= 0 {
			continue
		}
		server := findObject(state.Servers, service.ServerID)
		if server == nil {
			return errors.New("APPLICATION_NODE_OWNERSHIP_MISMATCH")
		}
		node, err := r.containerPeer(ctx, state, service.ServerID)
		if err != nil {
			return err
		}
		err = func() error {
			defer node.client.Close()
			desc, e := inspectContainerService(ctx, manager, service.ServiceName, appID, service.ServiceSlotKey)
			if e != nil {
				return e
			}
			if desc == nil {
				return errors.New("APPLICATION_SERVICE_NOT_DEPLOYED")
			}
			template := objectMapFromAny(objectMapFromAny(desc["Spec"])["TaskTemplate"])
			container := objectMapFromAny(template["ContainerSpec"])
			image := stringValue(container["Image"])
			raw, e := node.call(ctx, "docker image inspect --format '{{.Id}}' "+shellQuote(image), nil, "APPLICATION_CONTAINER_QUERY_FAILED")
			if e != nil {
				return e
			}
			if raw == "" {
				return errors.New("APPLICATION_CONTAINER_QUERY_FAILED")
			}
			return verifyContainerRelease(ctx, manager, node, domain.Object{"serviceName": service.ServiceName, "replicas": service.DesiredReplicas}, swarm, raw, int(numberValue(template["ForceUpdate"])))
		}()
		if err != nil {
			return err
		}
	}
	return nil
}
