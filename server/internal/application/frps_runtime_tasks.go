package application

import (
	"context"
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) executeFrpsRuntimeAction(ctx context.Context, task domain.Task, install bool) (string, error) {
	if r.FrpsRuntimeExecutor == nil || r.ServerRuntimeExecutor == nil {
		return "FRPS 执行器未配置", errors.New("SERVER_RUNTIME_EXECUTOR_NOT_CONFIGURED")
	}
	if !install {
		state, err := r.Store.LoadState(ctx)
		if err != nil {
			return "无法读取 FRPS 卸载影响", err
		}
		if _, err := frpsUninstallImpact(state, task.TargetID); err != nil {
			return "FRPS 卸载预检未通过", err
		}
	}
	input, err := r.serverRuntimeExecutionInput(ctx, task.TargetID, task)
	if err != nil {
		return "无法读取 FRPS 执行环境", err
	}
	failureCode, success := "FRPS_POST_UNINSTALL_PROBE_FAILED", "已卸载并核验 FRPS"
	if install {
		failureCode, success = "FRPS_POST_INSTALL_PROBE_FAILED", "已安装并核验 FRPS"
		err = r.FrpsRuntimeExecutor.InstallFrps(ctx, input, r.Config.FrpsPath, r.Config.FrpsImage)
	} else {
		err = r.FrpsRuntimeExecutor.UninstallFrps(ctx, input)
	}
	if err != nil {
		r.reconcileServerRuntimeAfterFailure(ctx, input, "frps", err)
		return "FRPS 操作失败", err
	}
	probe, err := r.refreshServerRuntimeAfterAction(ctx, input)
	if err != nil {
		return "FRPS 操作后核验失败", err
	}
	metrics := objectMapFromAny(probe["metrics"])
	verified := metrics["frps"] == true
	if !install {
		verified = metrics["frpsStatus"] == "missing" && metrics["frpsManagedPresent"] == false
	}
	if !verified {
		return "FRPS 操作后核验失败", errors.New(failureCode)
	}
	return success, nil
}

func frpsUninstallImpact(state domain.State, serverID string) (domain.Object, error) {
	impact := serverReferenceImpact{Reasons: []domain.Object{}, Counts: map[string]int{}}
	if findObject(state.Servers, serverID) == nil {
		return impact.Object(), errors.New("SERVER_NOT_FOUND")
	}
	if run := domain.ActiveFrpTunnelRunForServer(state.FrpTunnelRuns, serverID, ""); run != nil {
		impact.Reasons = append(impact.Reasons, domain.Object{
			"code": "SERVER_HAS_ACTIVE_TUNNEL_RUN", "resourceKind": "frpTunnelRun", "resourceId": run.ID, "field": "serverId",
		})
		impact.Counts["frpTunnelRun"] = 1
		return impact.Object(), errors.New("FRPS_UNINSTALL_BLOCKED_ACTIVE_TUNNEL")
	}
	return impact.Object(), nil
}
