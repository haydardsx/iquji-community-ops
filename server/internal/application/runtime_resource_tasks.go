package application

import (
	"context"
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) executeInstallTraefikGateway(ctx context.Context, task domain.Task) (string, error) {
	if r.RuntimeResourceExecutor == nil {
		return "runtime resource executor is not configured", errors.New("RUNTIME_RESOURCE_EXECUTOR_NOT_CONFIGURED")
	}
	input, err := r.traefikRuntimeExecutionInput(ctx, task.TargetID, task)
	if err != nil {
		return "failed to prepare Traefik install", err
	}
	_, err = r.RuntimeResourceExecutor.InstallTraefikGateway(ctx, input)
	if err != nil {
		_ = r.markServerRuntimeError(context.WithoutCancel(ctx), task.TargetID, "traefik", err)
		return "failed to install Traefik", err
	}
	base, err := r.serverRuntimeExecutionInput(ctx, task.TargetID, task)
	if err != nil {
		return "failed to prepare Traefik post-install probe", err
	}
	probe, err := r.refreshServerRuntimeAfterAction(ctx, base)
	if err != nil || objectMapFromAny(probe["metrics"])["traefik"] != true {
		if err == nil {
			err = errors.New("TRAEFIK_POST_INSTALL_PROBE_FAILED")
		}
		return "Traefik post-install probe failed", err
	}
	return "Traefik gateway installed and verified", nil
}

func (r *Runtime) executeUninstallTraefikGateway(ctx context.Context, task domain.Task) (string, error) {
	if r.RuntimeResourceExecutor == nil {
		return "runtime resource executor is not configured", errors.New("RUNTIME_RESOURCE_EXECUTOR_NOT_CONFIGURED")
	}
	input, err := r.traefikRuntimeExecutionInput(ctx, task.TargetID, task)
	if err != nil {
		return "failed to prepare Traefik uninstall", err
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return "读取 Jenkins 入口失败", err
	}
	for _, srv := range state.Servers {
		for _, binding := range domain.JenkinsAccessSnapshots(srv) {
			if binding["gatewayServerId"] == task.TargetID {
				return "请先解绑使用此网关的 Jenkins 入口", errors.New("JENKINS_ACCESS_GATEWAY_IN_USE")
			}
		}
	}
	_, err = r.RuntimeResourceExecutor.UninstallTraefikGateway(ctx, input)
	if err != nil {
		_ = r.markServerRuntimeError(context.WithoutCancel(ctx), task.TargetID, "traefik", err)
		return "failed to uninstall Traefik", err
	}
	base, err := r.serverRuntimeExecutionInput(ctx, task.TargetID, task)
	if err != nil {
		return "failed to prepare Traefik post-uninstall probe", err
	}
	probe, err := r.refreshServerRuntimeAfterAction(ctx, base)
	if err != nil || objectMapFromAny(probe["metrics"])["traefik"] == true {
		if err == nil {
			err = errors.New("TRAEFIK_POST_UNINSTALL_PROBE_FAILED")
		}
		return "Traefik post-uninstall probe failed", err
	}
	return "Traefik gateway uninstalled and verified", nil
}

func (r *Runtime) traefikRuntimeExecutionInput(ctx context.Context, serverID string, task domain.Task) (TraefikRuntimeExecutionInput, error) {
	base, err := r.serverRuntimeExecutionInput(ctx, serverID, task)
	if err != nil {
		return TraefikRuntimeExecutionInput{}, err
	}
	return TraefikRuntimeExecutionInput{

		CertResolver:        r.Config.TraefikCertResolver,
		Server:              base.Server,
		SSHKey:              base.SSHKey,
		Service:             r.Config.TraefikService,
		Image:               r.Config.TraefikImage,
		ImagePullCandidates: append([]string(nil), r.Config.TraefikImagePullCandidates...),
		Network:             r.Config.SwarmNetwork,
		HTTPPort:            r.Config.TraefikHTTPPort,
		HTTPSPort:           r.Config.TraefikHTTPSPort,
		AdvertiseAddr:       r.Config.SwarmAdvertiseAddr,
		LogStep:             base.LogStep,
	}, nil
}
