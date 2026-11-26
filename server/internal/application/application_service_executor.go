package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type ApplicationServiceTarget struct {
	ServiceSlotKey string
	ServiceName    string
	ServerID       string
	Replicas       int
}

type ApplicationServiceInput struct {
	Network              string
	Outputs              []domain.Object
	Environment          map[string]map[string]string `json:"-"`
	PreviousInputKeys    []string                     `json:"-"`
	ApplicationID        string
	Server               domain.Object
	SSHKey               string
	ClusterID            string
	VerifyLegacyLocation bool
	LegacyServiceIDs     []string
	Services             []ApplicationServiceTarget
	Servers              []domain.Object
	LogSecrets           []string `json:"-"`
}

type ApplicationServiceTask struct {
	ID       string `json:"id"`
	NodeID   string `json:"nodeId"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
	ServerID string `json:"serverId,omitempty"`
}

type ApplicationServiceState struct {
	ServiceSlotKey   string                   `json:"serviceSlotKey"`
	ServiceID        string                   `json:"serviceId"`
	ServiceName      string                   `json:"serviceName"`
	ServerID         string                   `json:"serverId"`
	ExpectedReplicas int                      `json:"expectedReplicas"`
	DesiredReplicas  int                      `json:"desiredReplicas"`
	RunningReplicas  int                      `json:"runningReplicas"`
	UpdateState      string                   `json:"updateState,omitempty"`
	Ports            []domain.Object          `json:"ports,omitempty"`
	LogDriver        string                   `json:"logDriver,omitempty"`
	Tasks            []ApplicationServiceTask `json:"tasks"`
}

type ApplicationRuntimeSnapshot struct {
	DeploymentManagerServerID string                    `json:"deploymentManagerServerId,omitempty"`
	DeploymentClusterID       string                    `json:"deploymentClusterId,omitempty"`
	Availability              string                    `json:"availability"`
	UnavailableReason         string                    `json:"unavailableReason,omitempty"`
	Status                    string                    `json:"status"`
	CheckedAt                 string                    `json:"checkedAt"`
	Services                  []ApplicationServiceState `json:"services"`
}

type ApplicationServiceLog struct {
	ID             string `json:"id"`
	Message        string `json:"message"`
	Timestamp      string `json:"timestamp,omitempty"`
	Source         string `json:"source"`
	ServiceSlotKey string `json:"serviceSlotKey"`
}

type ApplicationServiceLogs struct {
	Availability      string                  `json:"availability"`
	UnavailableReason string                  `json:"unavailableReason,omitempty"`
	CheckedAt         string                  `json:"checkedAt"`
	Truncated         bool                    `json:"truncated"`
	Items             []ApplicationServiceLog `json:"items"`
}

type ApplicationRuntimeExecutor interface {
	Inspect(context.Context, ApplicationServiceInput) (ApplicationRuntimeSnapshot, error)
	Apply(context.Context, ApplicationServiceInput, string, bool) (ApplicationRuntimeSnapshot, error)
	Logs(context.Context, ApplicationServiceInput, string) (ApplicationServiceLogs, error)
}

type applicationServiceRemote interface {
	Run(context.Context, string, int) (string, bool, error)
	RunLogs(context.Context, string, int) (string, bool, error)
	Close() error
}

type SystemApplicationRuntimeExecutor struct {
	dial func(context.Context, ApplicationServiceInput) (applicationServiceRemote, error)
}

func NewSystemApplicationRuntimeExecutor() *SystemApplicationRuntimeExecutor {
	return &SystemApplicationRuntimeExecutor{dial: openApplicationServiceRemote}
}

func (e *SystemApplicationRuntimeExecutor) Inspect(ctx context.Context, input ApplicationServiceInput) (ApplicationRuntimeSnapshot, error) {
	remote, err := e.dial(ctx, input)
	if err != nil {
		return ApplicationRuntimeSnapshot{}, err
	}
	defer func() { _ = remote.Close() }()
	return inspectApplicationServices(ctx, remote, input)
}

func inspectApplicationServices(ctx context.Context, remote applicationServiceRemote, input ApplicationServiceInput) (ApplicationRuntimeSnapshot, error) {
	return inspectApplicationServicesMode(ctx, remote, input, true)
}

func inspectApplicationServicesMode(ctx context.Context, remote applicationServiceRemote, input ApplicationServiceInput, includeTasks bool) (ApplicationRuntimeSnapshot, error) {
	snapshot := ApplicationRuntimeSnapshot{Availability: "available", Status: "not_deployed", CheckedAt: domain.Now(), Services: []ApplicationServiceState{}}
	output, _, err := remote.Run(ctx, "docker info --format '{{json .Swarm}}'", 65536)
	if err != nil {
		return snapshot, errors.New("APPLICATION_SWARM_QUERY_FAILED")
	}
	var swarm struct {
		LocalNodeState   string
		ControlAvailable bool
		Cluster          struct{ ID string }
	}
	if json.Unmarshal([]byte(output), &swarm) != nil || swarm.LocalNodeState != "active" || !swarm.ControlAvailable {
		return snapshot, errors.New("APPLICATION_SWARM_MANAGER_REQUIRED")
	}
	if swarm.Cluster.ID == "" || input.ClusterID != "" && swarm.Cluster.ID != input.ClusterID {
		return snapshot, errors.New("APPLICATION_SWARM_CLUSTER_MISMATCH")
	}
	snapshot.DeploymentManagerServerID = stringValue(input.Server["id"])
	snapshot.DeploymentClusterID = swarm.Cluster.ID
	command := "docker service ls --filter " + shellQuote("label=iquops.owner=iquops") + " --filter " + shellQuote("label=iquops.application-id="+input.ApplicationID) + " --format '{{.ID}}'"
	ids, truncated, err := remote.Run(ctx, command, 65536)
	if err != nil || truncated {
		return snapshot, errors.New("APPLICATION_SERVICES_QUERY_FAILED")
	}
	if strings.TrimSpace(ids) == "" {
		if input.VerifyLegacyLocation {
			return snapshot, errors.New("APPLICATION_RUNTIME_CHECK_REQUIRED")
		}
		snapshot.Availability = "not_deployed"
		return snapshot, nil
	}
	command = "docker service inspect " + shellQuotedWords(strings.Fields(ids))
	output, truncated, err = remote.Run(ctx, command, 4*1024*1024)
	if err != nil || truncated {
		return snapshot, errors.New("APPLICATION_SERVICES_QUERY_FAILED")
	}
	var services []domain.Object
	if json.Unmarshal([]byte(output), &services) != nil {
		return snapshot, errors.New("APPLICATION_SERVICES_QUERY_FAILED")
	}
	if input.VerifyLegacyLocation && len(input.LegacyServiceIDs) > 0 {
		proven := false
		for _, service := range services {
			if containsString(input.LegacyServiceIDs, stringValue(service["ID"])) {
				proven = true
				break
			}
		}
		if !proven {
			return snapshot, errors.New("APPLICATION_RUNTIME_CHECK_REQUIRED")
		}
	}
	for _, service := range services {
		state, err := inspectApplicationServiceMode(ctx, remote, input, service, includeTasks)
		if err != nil {
			return snapshot, err
		}
		snapshot.Services = append(snapshot.Services, state)
	}
	snapshot.CheckedAt = domain.Now()
	snapshot.Status = applicationRuntimeStatus(snapshot.Services)
	return snapshot, nil
}

func inspectApplicationService(ctx context.Context, remote applicationServiceRemote, input ApplicationServiceInput, service domain.Object) (ApplicationServiceState, error) {
	return inspectApplicationServiceMode(ctx, remote, input, service, true)
}

func inspectApplicationServiceMode(ctx context.Context, remote applicationServiceRemote, input ApplicationServiceInput, service domain.Object, includeTasks bool) (ApplicationServiceState, error) {
	spec := objectMapFromAny(service["Spec"])
	labels := objectMapFromAny(spec["Labels"])
	state := ApplicationServiceState{ServiceID: stringValue(service["ID"]), ServiceName: stringValue(spec["Name"]), ServiceSlotKey: stringValue(labels["iquops.service-slot"]), Tasks: []ApplicationServiceTask{}}
	if labels["iquops.owner"] != "iquops" || labels["iquops.application-id"] != input.ApplicationID || state.ServiceID == "" {
		return state, errors.New("APPLICATION_SERVICE_OWNERSHIP_MISMATCH")
	}
	mode := objectMapFromAny(spec["Mode"])
	if _, ok := mode["Replicated"]; !ok {
		return state, errors.New("APPLICATION_SERVICE_MODE_UNSUPPORTED")
	}
	state.DesiredReplicas = int(numberValue(objectMapFromAny(mode["Replicated"])["Replicas"]))
	state.UpdateState = stringValue(objectMapFromAny(service["UpdateStatus"])["State"])
	state.Ports = observedServicePorts(labels, objectMapFromAny(service["Endpoint"]))
	state.LogDriver = stringValue(objectMapFromAny(objectMapFromAny(spec["TaskTemplate"])["LogDriver"])["Name"])
	for _, target := range input.Services {
		if target.ServiceName == state.ServiceName {
			state.ExpectedReplicas, state.ServerID = target.Replicas, target.ServerID
			if state.ServiceSlotKey == "" {
				state.ServiceSlotKey = target.ServiceSlotKey
			}
		}
	}
	if !includeTasks {
		return state, nil
	}
	// 只读取当前期望状态的任务；Swarm 会保留历史 shutdown/rejected 任务，
	// 把它们混入运行态会把旧镜像错误误报成当前服务故障。
	ids, truncated, err := remote.Run(ctx, "docker service ps --filter "+shellQuote("desired-state=running")+" --no-trunc -q "+shellQuote(state.ServiceID), 65536)
	if err != nil || truncated {
		return state, errors.New("APPLICATION_TASKS_QUERY_FAILED")
	}
	if strings.TrimSpace(ids) == "" {
		return state, nil
	}
	output, truncated, err := remote.Run(ctx, "docker inspect --type task "+shellQuotedWords(strings.Fields(ids)), 4*1024*1024)
	if err != nil || truncated {
		return state, errors.New("APPLICATION_TASKS_QUERY_FAILED")
	}
	var tasks []domain.Object
	if json.Unmarshal([]byte(output), &tasks) != nil {
		return state, errors.New("APPLICATION_TASKS_QUERY_FAILED")
	}
	for _, task := range tasks {
		status := objectMapFromAny(task["Status"])
		item := ApplicationServiceTask{ID: stringValue(task["ID"]), NodeID: stringValue(task["NodeID"]), Status: stringValue(status["State"]), Error: domain.LogSummary(stringValue(status["Err"]))}
		for _, server := range input.Servers {
			if stringValue(serverRuntimeMetrics(server)["swarmNodeId"]) == item.NodeID && item.NodeID != "" {
				item.ServerID = stringValue(server["id"])
				break
			}
		}
		if item.Status == "running" {
			state.RunningReplicas++
		}
		state.Tasks = append(state.Tasks, item)
	}
	return state, nil
}

func applicationRuntimeStatus(services []ApplicationServiceState) string {
	if len(services) == 0 {
		return "not_deployed"
	}
	allStopped, allRunning := true, true
	for _, service := range services {
		allStopped = allStopped && service.DesiredReplicas == 0 && service.RunningReplicas == 0
		allRunning = allRunning && service.DesiredReplicas > 0 && service.RunningReplicas == service.DesiredReplicas && service.UpdateState != "paused" && service.UpdateState != "rollback_paused"
	}
	if allStopped {
		return "stopped"
	}
	if allRunning {
		return "running"
	}
	return "degraded"
}
