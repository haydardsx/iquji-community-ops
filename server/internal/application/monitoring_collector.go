package application

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

const monitoringPollInterval = 30 * time.Second
const monitoringPollTimeout = 30 * time.Second
const monitoringPollLease = 45 * time.Second

type monitoringCollection struct {
	Report       domain.Object
	SwarmMetrics domain.Object
}

type monitoringReportCollector interface {
	CollectMonitoringReport(context.Context, ServerRuntimeExecutionInput) (monitoringCollection, error)
}

func (e *SystemServerRuntimeExecutor) CollectMonitoringReport(ctx context.Context, input ServerRuntimeExecutionInput) (monitoringCollection, error) {
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return monitoringCollection{}, errors.New("MONITORING_AGENT_SSH_UNAVAILABLE")
	}
	defer func() { _ = client.Close() }()
	report, err := remoteMonitoringAgentReport(ctx, client)
	if err != nil {
		return monitoringCollection{}, errors.New("MONITORING_AGENT_REPORT_UNAVAILABLE")
	}
	result := domain.Object{"metrics": domain.Object{}, "checks": []domain.Object{}}
	applyRemoteSwarmProbe(ctx, client, input, result)
	return monitoringCollection{Report: report, SwarmMetrics: objectMapFromAny(result["metrics"])}, nil
}

// RunMonitoringCollector 独立采集已安装 Agent，任务执行循环无需等待 SSH 和采样。
func (r *Runtime) RunMonitoringCollector(ctx context.Context) {
	ticker := time.NewTicker(monitoringPollInterval)
	defer ticker.Stop()
	for {
		if err := r.CollectMonitoringOnce(ctx); err != nil && ctx.Err() == nil {
			platform.Diagnostic("worker", "warn", "monitoring_failed", "监控采集未完成", stableErrorCode(err, "MONITORING_COLLECTION_FAILED"))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Runtime) CollectMonitoringOnce(ctx context.Context) error {
	ctx = r.WithServerConnections(ctx)
	collector, ok := r.ServerRuntimeExecutor.(monitoringReportCollector)
	if !ok {
		return nil
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return err
	}
	var pending sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, agent := range state.MonitoringAgents {
		serverID := stringValue(agent["serverId"])
		if !monitoringAgentInstalled(agent) || findObject(state.Servers, serverID) == nil {
			continue
		}
		select {
		case <-ctx.Done():
			pending.Wait()
			return ctx.Err()
		case slots <- struct{}{}:
		}
		pending.Add(1)
		go func() {
			defer pending.Done()
			defer func() { <-slots }()
			if err := r.collectMonitoringServer(ctx, serverID, collector); err != nil && ctx.Err() == nil {
				platform.Diagnostic("worker", "warn", "monitoring_server_failed", "服务器监控采集未完成", stableErrorCode(err, "MONITORING_COLLECTION_FAILED"))
			}
		}()
	}
	pending.Wait()
	return ctx.Err()
}

func monitoringAgentInstalled(agent domain.Object) bool {
	status := stringValue(agent["status"])
	return status != "" && status != "not_installed" && status != "uninstalled" && status != "installing"
}

func monitoringAgentOperationActive(state domain.State, serverID string) bool {
	for _, task := range state.Tasks {
		if task.TargetID == serverID && (task.Action == "install_monitoring_agent" || task.Action == "uninstall_monitoring_agent") && (task.Status == "executing" || task.Status == "running" || task.Status == "queued") {
			return true
		}
	}
	return false
}

func (r *Runtime) claimMonitoringPoll(ctx context.Context, serverID string) (string, error) {
	token := ""
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		agent := findObjectBy(state.MonitoringAgents, "serverId", serverID)
		if findObject(state.Servers, serverID) == nil || !monitoringAgentInstalled(agent) || monitoringAgentOperationActive(*state, serverID) {
			return nil
		}
		now := time.Now()
		lease, _ := time.Parse(time.RFC3339Nano, stringValue(agent["pollLeaseUntil"]))
		lastPoll, _ := time.Parse(time.RFC3339Nano, stringValue(agent["lastPollAt"]))
		if lease.After(now) || now.Sub(lastPoll) < monitoringPollInterval {
			return nil
		}
		token = domain.NewID("poll")
		agent["pollToken"] = token
		agent["pollLeaseUntil"] = now.Add(monitoringPollLease).UTC().Format(time.RFC3339Nano)
		agent["lastPollAt"] = now.UTC().Format(time.RFC3339Nano)
		return nil
	})
	return token, err
}

func (r *Runtime) collectMonitoringServer(ctx context.Context, serverID string, collector monitoringReportCollector) error {
	token, err := r.claimMonitoringPoll(ctx, serverID)
	if err != nil || token == "" {
		return err
	}
	pollCtx, cancel := context.WithTimeout(ctx, monitoringPollTimeout)
	defer cancel()
	input, err := r.serverRuntimeExecutionInput(pollCtx, serverID, domain.Task{})
	var collected monitoringCollection
	if err == nil {
		collected, err = collector.CollectMonitoringReport(pollCtx, input)
	}
	if err == nil && (stringValue(collected.Report["serverId"]) != serverID || stringValue(collected.Report["sampledAt"]) == "") {
		err = errors.New("MONITORING_AGENT_REPORT_INVALID")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return r.finishMonitoringPoll(ctx, serverID, token, collected, err)
}

func (r *Runtime) finishMonitoringPoll(ctx context.Context, serverID, token string, collected monitoringCollection, pollErr error) error {
	return r.Store.MutateState(ctx, func(state *domain.State) error {
		agent := findObjectBy(state.MonitoringAgents, "serverId", serverID)
		if agent["pollToken"] != token || !monitoringAgentInstalled(agent) || findObject(state.Servers, serverID) == nil {
			return nil
		}
		delete(agent, "pollToken")
		delete(agent, "pollLeaseUntil")
		if monitoringAgentOperationActive(*state, serverID) {
			return nil
		}
		if pollErr != nil {
			if observedAfter(stringValue(agent["lastHeartbeatAt"]), stringValue(agent["lastPollAt"])) {
				return nil
			}
			if pollErr.Error() == "MONITORING_AGENT_SSH_UNAVAILABLE" {
				applyMonitoringSwarmMetrics(state, serverID, domain.Object{"swarm": false}, stringValue(agent["lastPollAt"]))
			}
			agent["status"] = "offline"
			agent["lastError"] = stableErrorCode(pollErr, "MONITORING_AGENT_REPORT_UNAVAILABLE")
			agent["updatedAt"] = domain.Now()
			return nil
		}
		// 一次较早发起的采集不得覆盖更新的主动探测或新安装实例。
		report := collected.Report
		if !observedAfter(stringValue(agent["lastSampleAt"]), stringValue(report["sampledAt"])) {
			report = cloneObject(report)
			report["lastError"] = ""
			applyMonitoringReportToState(state, report)
		}
		if len(collected.SwarmMetrics) > 0 {
			applyMonitoringSwarmMetrics(state, serverID, collected.SwarmMetrics, stringValue(agent["lastPollAt"]))
		}
		return nil
	})
}

func clearMonitoringPoll(agent domain.Object) {
	delete(agent, "pollToken")
	delete(agent, "pollLeaseUntil")
	delete(agent, "lastPollAt")
}

// 周期采样只更新 Swarm 事实，保留完整运行时探测的时间和结论。
func applyMonitoringSwarmMetrics(state *domain.State, serverID string, incoming domain.Object, startedAt string) {
	server := findObject(state.Servers, serverID)
	if server == nil {
		return
	}
	health := objectMapFromAny(server["health"])
	metrics := objectMapFromAny(health["metrics"])
	mergeSwarmProbeMetrics(metrics, incoming, startedAt)
	health["metrics"] = metrics
	server["health"] = health
}
