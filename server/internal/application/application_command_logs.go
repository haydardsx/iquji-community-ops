package application

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// ApplicationCommandLogs 只执行已保存的查询；调用方只能选择应用、服务与运行副本。
func (r *Runtime) ApplicationCommandLogs(ctx context.Context, applicationID, slot, taskID string) (ApplicationServiceLogs, error) {
	ctx, cancel := context.WithTimeout(r.WithServerConnections(ctx), applicationLogTimeout)
	defer cancel()
	result := ApplicationServiceLogs{Availability: "unavailable", CheckedAt: domain.Now(), Items: []ApplicationServiceLog{}}
	err := r.readApplicationCommandLogs(ctx, applicationID, slot, taskID, &result)
	if err == nil {
		result.Availability = "available"
		result.CheckedAt = domain.Now()
		boundApplicationServiceLogResponse(&result)
		return result, nil
	}
	if err.Error() == "APPLICATION_NOT_FOUND" {
		return result, err
	}
	result.UnavailableReason = applicationUnavailableReason(err)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.UnavailableReason = "APPLICATION_LOG_COMMAND_TIMEOUT"
	}
	if err.Error() == "APPLICATION_DEPLOYMENT_MANAGER_REQUIRED" || err.Error() == "APPLICATION_NOT_DEPLOYED" {
		result.Availability = "not_deployed"
	}
	return result, nil
}

func (r *Runtime) readApplicationCommandLogs(ctx context.Context, applicationID, slot, taskID string, result *ApplicationServiceLogs) error {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return err
	}
	app := findObject(state.Applications, applicationID)
	if app == nil {
		return errors.New("APPLICATION_NOT_FOUND")
	}
	config := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", applicationID)
	command, ok := stringArguments(objectMapFromAny(config["swarmRuntime"])["logCommand"])
	if !ok {
		return errors.New("APPLICATION_LOG_COMMAND_MISSING")
	}
	input, err := r.applicationServiceInput(ctx, applicationID, config)
	if err != nil {
		return err
	}
	if r.ApplicationRuntimeExecutor == nil {
		return errors.New("APPLICATION_EXECUTOR_UNAVAILABLE")
	}
	snapshot, err := r.ApplicationRuntimeExecutor.Inspect(ctx, input)
	if err != nil {
		return err
	}
	if snapshot.Availability == "not_deployed" {
		return errors.New("APPLICATION_NOT_DEPLOYED")
	}
	target, err := applicationLogTarget(snapshot, applicationID, slot, taskID)
	if err != nil {
		return err
	}
	server := findObject(state.Servers, target.ServerID)
	if server == nil || !serverAvailableToProject(server, stringValue(app["projectId"])) {
		return errors.New("APPLICATION_LOG_REPLICA_CHANGED")
	}
	secrets, err := r.applicationLogSecrets(ctx, applicationID, slot)
	if err != nil {
		return err
	}
	key, ok := r.serverSSHSecretMaterial(ctx, state, server)
	if !ok {
		return errors.New("SERVER_SSH_SECRET_MATERIAL_REQUIRED")
	}
	client, err := r.openTerminalSSH(ctx, server, key)
	if err != nil {
		return err
	}
	defer client.Close()
	return runApplicationLogCommand(ctx, &applicationSSHRemote{client: client}, target, slot, command, secrets, result)
}

func applicationLogTarget(snapshot ApplicationRuntimeSnapshot, applicationID, slot, taskID string) (TerminalTarget, error) {
	for _, service := range snapshot.Services {
		if service.ServiceSlotKey != slot {
			continue
		}
		running := false
		for _, task := range service.Tasks {
			if task.Status != "running" {
				continue
			}
			running = true
			if task.ID == taskID && task.NodeID != "" && task.ServerID != "" {
				return TerminalTarget{ServerID: task.ServerID, ApplicationID: applicationID, ServiceID: service.ServiceID, TaskID: task.ID, NodeID: task.NodeID}, nil
			}
		}
		if !running {
			return TerminalTarget{}, errors.New("APPLICATION_LOG_NO_RUNNING_REPLICA")
		}
		if taskID == "" {
			return TerminalTarget{}, errors.New("APPLICATION_LOG_REPLICA_REQUIRED")
		}
		return TerminalTarget{}, errors.New("APPLICATION_LOG_REPLICA_CHANGED")
	}
	return TerminalTarget{}, errors.New("APPLICATION_LOG_SERVICE_REQUIRED")
}

// 限时器在容器内运行，用户关闭窗口或 SSH 连接中断时仍能回收查询进程。
func runApplicationLogCommand(ctx context.Context, remote applicationServiceRemote, target TerminalTarget, slot string, argv, secrets []string, result *ApplicationServiceLogs) error {
	id, err := resolveSwarmContainerID(ctx, remote, target)
	if err != nil {
		return errors.New("APPLICATION_LOG_REPLICA_CHANGED")
	}
	prefix := "docker exec " + id + " timeout "
	if _, _, err = remote.Run(ctx, prefix+"--help", 8192); err != nil {
		return errors.New("APPLICATION_LOG_TIMEOUT_UNAVAILABLE")
	}
	deadline, _ := ctx.Deadline()
	seconds := int(time.Until(deadline) / time.Second)
	if seconds < 1 {
		return errors.New("APPLICATION_LOG_COMMAND_TIMEOUT")
	}
	words := make([]string, len(argv))
	for i, arg := range argv {
		words[i] = shellQuote(arg)
	}
	output, truncated, err := remote.RunLogs(ctx, prefix+"-s KILL "+strconv.Itoa(seconds)+" "+strings.Join(words, " "), applicationLogMaxBytes)
	if err != nil {
		var exit interface{ ExitStatus() int }
		if ctx.Err() != nil || errors.As(err, &exit) && (exit.ExitStatus() == 124 || exit.ExitStatus() == 137) {
			return errors.New("APPLICATION_LOG_COMMAND_TIMEOUT")
		}
		return errors.New("APPLICATION_LOG_COMMAND_FAILED")
	}
	result.Items, result.Truncated = parseApplicationServiceLogs(output, ApplicationServiceState{ServiceID: target.TaskID, ServiceSlotKey: slot}, secrets, truncated)
	for i := range result.Items {
		result.Items[i].Source = "application"
	}
	return nil
}
