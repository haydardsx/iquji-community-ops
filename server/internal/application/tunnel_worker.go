package application

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) executeStartFrpTunnel(ctx context.Context, task domain.Task) (string, error) {
	input, err := r.frpTunnelExecutionInput(ctx, task.TargetID, task, true)
	if err != nil {
		_ = r.patchFrpTunnelRun(ctx, task.TargetID, func(run *domain.FrpTunnelRun) {
			run.Status = frpTunnelStatusFailed
			run.ErrorSummary = stableErrorCode(err, "FRP_TUNNEL_OPERATION_FAILED")
		})
		return "failed to prepare frp tunnel", err
	}
	if input.Run.Status == frpTunnelStatusStopped || input.Run.Status == frpTunnelStatusCancelled {
		return "frp tunnel run is already inactive", nil
	}
	if r.FrpExecutor == nil {
		err := errors.New("FRP_EXECUTOR_NOT_CONFIGURED")
		_ = r.patchFrpTunnelRun(ctx, task.TargetID, func(run *domain.FrpTunnelRun) {
			run.Status = frpTunnelStatusFailed
			run.ErrorSummary = stableErrorCode(err, "FRP_TUNNEL_OPERATION_FAILED")
		})
		return "frp executor is not configured", err
	}
	if err := r.patchFrpTunnelRun(ctx, task.TargetID, func(run *domain.FrpTunnelRun) {
		run.Status = frpTunnelStatusStarting
		run.ManagedFrps = true
		run.StartOperationRunID = task.ID
		run.ErrorSummary = ""
	}); err != nil {
		return "failed to record frp tunnel start", err
	}
	input.Run.ManagedFrps = true
	input.OnFrpcExit = func() { r.markExitedFrpc(input.Run.ID, "FRPC_PROCESS_EXITED") }
	r.appendRuntimeMessage(task, "worker", "start", "info", "正在启动穿透运行")
	if err := r.FrpExecutor.StartFrpTunnel(ctx, input); err != nil {
		status := frpTunnelStatusFailed
		var cleanupErr error
		var inactive frpStartNotActivatedError
		if !errors.As(err, &inactive) && !isLocalTCPPortUnreachable(err) {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			cleanupErr = r.FrpExecutor.CleanupFrpTunnel(cleanupCtx, input)
			cancel()
			if cleanupErr != nil {
				status = frpTunnelStatusCleanupRequired
			}
		}
		if cleanupErr == nil {
			_ = r.patchFrpTunnelRunWithSecretDeletion(context.WithoutCancel(ctx), task.TargetID, input.Run.TokenSecretRef, func(run *domain.FrpTunnelRun) {
				run.Status = status
				run.ErrorSummary = stableErrorCode(err, "FRP_TUNNEL_OPERATION_FAILED")
				run.FailedAt = domain.Now()
			})
		} else {
			_ = r.patchFrpTunnelRun(context.WithoutCancel(ctx), task.TargetID, func(run *domain.FrpTunnelRun) {
				run.Status = status
				run.ErrorSummary = stableErrorCode(err, "FRP_TUNNEL_OPERATION_FAILED")
				run.FailedAt = domain.Now()
			})
		}
		return "failed to start frp tunnel", err
	}
	now := domain.Now()
	if err := r.patchFrpTunnelRun(context.WithoutCancel(ctx), task.TargetID, func(run *domain.FrpTunnelRun) {
		run.Status = frpTunnelStatusRunning
		run.StartedAt = now
		run.LastVerifiedAt = now
		run.ErrorSummary = ""
	}); err != nil {
		return "failed to record frp tunnel readiness", err
	}
	if executor, ok := r.FrpExecutor.(*SystemFrpTunnelExecutor); ok && !executor.frpcRunning(input.Run.ID) {
		input.OnFrpcExit()
		return "frpc exited while starting", errors.New("FRPC_PROCESS_EXITED")
	}
	return "frp tunnel is running", nil
}

func (r *Runtime) executeStopFrpTunnel(ctx context.Context, task domain.Task) (string, error) {
	input, err := r.frpTunnelExecutionInput(ctx, task.TargetID, task, false)
	if err != nil {
		_ = r.patchFrpTunnelRun(ctx, task.TargetID, func(run *domain.FrpTunnelRun) {
			run.Status = frpTunnelStatusCleanupRequired
			run.ErrorSummary = stableErrorCode(err, "FRP_TUNNEL_OPERATION_FAILED")
		})
		return "failed to prepare frp tunnel stop", err
	}
	if input.Run.Status == frpTunnelStatusStopped || input.Run.Status == frpTunnelStatusCancelled {
		return "frp tunnel run is already inactive", nil
	}
	if r.FrpExecutor == nil {
		err := errors.New("FRP_EXECUTOR_NOT_CONFIGURED")
		_ = r.patchFrpTunnelRun(ctx, task.TargetID, func(run *domain.FrpTunnelRun) {
			run.Status = frpTunnelStatusCleanupRequired
			run.ErrorSummary = stableErrorCode(err, "FRP_TUNNEL_OPERATION_FAILED")
		})
		return "frp executor is not configured", err
	}
	_ = r.patchFrpTunnelRun(ctx, task.TargetID, func(run *domain.FrpTunnelRun) {
		run.Status = frpTunnelStatusStopping
		run.StopOperationRunID = task.ID
	})
	r.appendRuntimeMessage(task, "worker", "stop", "info", "正在停止穿透运行")
	if err := r.FrpExecutor.StopFrpTunnel(ctx, input); err != nil {
		_ = r.patchFrpTunnelRun(context.WithoutCancel(ctx), task.TargetID, func(run *domain.FrpTunnelRun) {
			run.Status = frpTunnelStatusCleanupRequired
			run.ErrorSummary = stableErrorCode(err, "FRP_TUNNEL_OPERATION_FAILED")
		})
		return "failed to stop frp tunnel", err
	}
	return r.markFrpTunnelRunStopped(context.WithoutCancel(ctx), input.Run, "stopped")
}

func (r *Runtime) executeDeleteFrpTunnel(ctx context.Context, task domain.Task) (string, error) {
	err := r.Store.MutateStateAndDeleteFrpTunnelData(ctx, task.TargetID, func(state *domain.State) error {
		if domain.FindFrpTunnel(state.FrpTunnels, task.TargetID) == nil {
			return errors.New("FRP_TUNNEL_NOT_FOUND")
		}
		if frpTunnelHasBlockingRun(state.FrpTunnelRuns, task.TargetID) {
			return errors.New("FRP_TUNNEL_NOT_DELETABLE")
		}
		tunnels := make([]domain.FrpTunnel, 0, len(state.FrpTunnels))
		for _, tunnel := range state.FrpTunnels {
			if tunnel.ID != task.TargetID {
				tunnels = append(tunnels, tunnel)
			}
		}
		runs := make([]domain.FrpTunnelRun, 0, len(state.FrpTunnelRuns))
		for _, run := range state.FrpTunnelRuns {
			if run.TunnelID != task.TargetID {
				runs = append(runs, run)
			}
		}
		state.FrpTunnels = tunnels
		state.FrpTunnelRuns = runs
		return nil
	})
	if err != nil {
		return "failed to delete frp tunnel", err
	}
	return "frp tunnel deleted", nil
}

func (r *Runtime) RecoverFrpTunnels(ctx context.Context) error {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return err
	}
	for _, run := range state.FrpTunnelRuns {
		if run.ManagedFrps && run.Status == frpTunnelStatusRunning {
			r.markExitedFrpc(run.ID, "FRPC_WORKER_RESTARTED")
		}
	}
	return ctx.Err()
}

func (r *Runtime) ShutdownFrpTunnels(ctx context.Context) error {
	executor, ok := r.FrpExecutor.(*SystemFrpTunnelExecutor)
	if !ok {
		return nil
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return err
	}
	for _, run := range state.FrpTunnelRuns {
		if !executor.frpcRunning(run.ID) {
			continue
		}
		executor.stopLocalFrpc(withFrpLogStep(FrpTunnelExecutionInput{Run: run}))
		r.markExitedFrpc(run.ID, "FRPC_WORKER_STOPPED")
	}
	return ctx.Err()
}

func (r *Runtime) markExitedFrpc(runID, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = r.Store.MutateState(ctx, func(state *domain.State) error {
		run := domain.FindFrpTunnelRun(state.FrpTunnelRuns, runID)
		if run != nil && run.Status == frpTunnelStatusRunning {
			run.Status = frpTunnelStatusCleanupRequired
			run.ErrorSummary = reason
			run.FailedAt = domain.Now()
			run.UpdatedAt = run.FailedAt
		}
		return nil
	})
}

func (r *Runtime) frpTunnelExecutionInput(ctx context.Context, runID string, task domain.Task, requireToken bool) (FrpTunnelExecutionInput, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return FrpTunnelExecutionInput{}, err
	}
	run := domain.FindFrpTunnelRun(state.FrpTunnelRuns, runID)
	if run == nil {
		return FrpTunnelExecutionInput{}, errors.New("FRP_TUNNEL_RUN_NOT_FOUND")
	}
	return r.frpTunnelExecutionInputFromState(ctx, state, *run, task, requireToken)
}

func (r *Runtime) frpTunnelExecutionInputFromState(ctx context.Context, state domain.State, run domain.FrpTunnelRun, task domain.Task, requireToken bool) (FrpTunnelExecutionInput, error) {
	if requireToken {
		if reasons := frpTunnelAssetBlockers(state, run.Mappings); len(reasons) > 0 {
			return FrpTunnelExecutionInput{}, errors.New("FRP_TUNNEL_PREVIEW_BLOCKED:" + strings.Join(reasons, ","))
		}
	}
	server := findObject(state.Servers, run.ServerID)
	if server == nil {
		return FrpTunnelExecutionInput{}, errors.New("SERVER_NOT_FOUND")
	}
	sshKey, ok := r.serverSSHSecretMaterial(ctx, state, server)
	if !ok {
		return FrpTunnelExecutionInput{}, errors.New("SERVER_SSH_SECRET_MATERIAL_REQUIRED")
	}
	token := ""
	if run.TokenSecretRef != "" {
		value, err := r.Store.GetSecretMaterial(ctx, run.TokenSecretRef)
		if err == nil {
			token = value
		} else if requireToken {
			return FrpTunnelExecutionInput{}, err
		}
	} else if requireToken {
		return FrpTunnelExecutionInput{}, errors.New("FRP_TOKEN_SECRET_REQUIRED")
	}
	proxyURL, err := r.frpcProxyURL(ctx, server)
	if err != nil {
		return FrpTunnelExecutionInput{}, err
	}
	return FrpTunnelExecutionInput{

		CertResolver:     r.Config.TraefikCertResolver,
		proxyURL:         proxyURL,
		Run:              run,
		Server:           cloneObject(server),
		SSHKey:           sshKey,
		Token:            token,
		FrpcPath:         r.Config.FrpcPath,
		FrpcDNSServer:    r.Config.FrpcDNSServer,
		StateDir:         r.Config.StateDir,
		RepositoryRoot:   r.Config.RepositoryRoot,
		TraefikNetwork:   r.Config.SwarmNetwork,
		TraefikHTTPPort:  r.Config.TraefikHTTPPort,
		TraefikHTTPSPort: r.Config.TraefikHTTPSPort,
		LogStep: func(source string, phase string, level string, message string) {
			r.appendRuntimeMessage(task, source, phase, level, message)
		},
	}, nil
}

func (r *Runtime) markFrpTunnelRunStopped(ctx context.Context, run domain.FrpTunnelRun, message string) (string, error) {
	err := r.patchFrpTunnelRunWithSecretDeletion(ctx, run.ID, run.TokenSecretRef, func(current *domain.FrpTunnelRun) {
		current.Status = frpTunnelStatusStopped
		current.StoppedAt = domain.Now()
		current.ErrorSummary = ""
	})
	return message, err
}

func (r *Runtime) patchFrpTunnelRun(ctx context.Context, runID string, mutate func(*domain.FrpTunnelRun)) error {
	return r.patchFrpTunnelRunWithSecretDeletion(ctx, runID, "", mutate)
}

func (r *Runtime) patchFrpTunnelRunWithSecretDeletion(ctx context.Context, runID string, deleteSecretRef string, mutate func(*domain.FrpTunnelRun)) error {
	deleteRefs := []string{}
	if deleteSecretRef != "" {
		deleteRefs = append(deleteRefs, deleteSecretRef)
	}
	return r.Store.MutateStateWithSecretChanges(ctx, nil, deleteRefs, func(state *domain.State) error {
		run := domain.FindFrpTunnelRun(state.FrpTunnelRuns, runID)
		if run == nil {
			return errors.New("FRP_TUNNEL_RUN_NOT_FOUND")
		}
		mutate(run)
		run.UpdatedAt = domain.Now()
		return nil
	})
}

func isLocalTCPPortUnreachable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "LOCAL_TCP_PORT_UNREACHABLE")
}

func intString(value int) string {
	return strconv.Itoa(value)
}
