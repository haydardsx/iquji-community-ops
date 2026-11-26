package application

import (
	"context"
	"errors"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const (
	frpTunnelStatusPendingConfirmation = domain.FrpTunnelStatusPendingConfirmation
	frpTunnelStatusStarting            = domain.FrpTunnelStatusStarting
	frpTunnelStatusRunning             = domain.FrpTunnelStatusRunning
	frpTunnelStatusStopping            = domain.FrpTunnelStatusStopping
	frpTunnelStatusStopped             = domain.FrpTunnelStatusStopped
	frpTunnelStatusFailed              = domain.FrpTunnelStatusFailed
	frpTunnelStatusCleanupRequired     = domain.FrpTunnelStatusCleanupRequired
	frpTunnelStatusCancelled           = domain.FrpTunnelStatusCancelled
)

var frpTunnelMappingNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type FrpTunnelExecutor interface {
	StartFrpTunnel(ctx context.Context, input FrpTunnelExecutionInput) error
	StopFrpTunnel(ctx context.Context, input FrpTunnelExecutionInput) error
	CleanupFrpTunnel(ctx context.Context, input FrpTunnelExecutionInput) error
}

type FrpTunnelExecutionInput struct {
	CertResolver     string
	proxyURL         string
	Run              domain.FrpTunnelRun
	Server           domain.Object
	SSHKey           string
	Token            string
	FrpcPath         string
	FrpcDNSServer    string
	StateDir         string
	RepositoryRoot   string
	TraefikNetwork   string
	TraefikHTTPPort  int
	TraefikHTTPSPort int
	LogStep          func(source string, phase string, level string, message string)
	OnFrpcExit       func()
}

type frpTunnelDraft struct {
	Name         string
	ServerID     string
	FrpsBindPort int
	Mappings     []domain.FrpTunnelMapping
}

func (r *Runtime) ListFrpTunnels(ctx context.Context) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := r.Store.ListFrpProbeHitCounts(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]domain.Object, 0, len(state.FrpTunnels))
	for _, tunnel := range state.FrpTunnels {
		items = append(items, r.publicFrpTunnel(tunnel, state, counts[tunnel.ID]))
	}
	sort.SliceStable(items, func(i, j int) bool {
		return publicTunnelSortTime(items[i]) > publicTunnelSortTime(items[j])
	})
	return page(items), nil
}

func (r *Runtime) GetFrpTunnel(ctx context.Context, id string) (domain.Object, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	tunnel := domain.FindFrpTunnel(state.FrpTunnels, id)
	if tunnel == nil {
		return nil, errors.New("FRP_TUNNEL_NOT_FOUND")
	}
	hitCount, err := r.Store.FrpProbeHitCount(ctx, id)
	if err != nil {
		return nil, err
	}
	return r.publicFrpTunnel(*tunnel, state, hitCount), nil
}

func (r *Runtime) ListFrpTunnelRuns(ctx context.Context, tunnelID string, pageNumber int, pageSize int) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	if domain.FindFrpTunnel(state.FrpTunnels, tunnelID) == nil {
		return nil, errors.New("FRP_TUNNEL_NOT_FOUND")
	}
	if pageNumber <= 0 {
		pageNumber = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	runs := domain.FrpTunnelRunsFor(state.FrpTunnelRuns, tunnelID)
	total := len(runs)
	start := min((pageNumber-1)*pageSize, total)
	end := min(start+pageSize, total)
	items := make([]domain.Object, 0, end-start)
	for _, run := range runs[start:end] {
		items = append(items, publicFrpTunnelRun(run))
	}
	return map[string]any{"items": items, "page": pageNumber, "pageSize": pageSize, "total": total}, nil
}

func (r *Runtime) PreviewFrpTunnel(ctx context.Context, input domain.FrpTunnelInput) (map[string]any, error) {
	return r.PreviewFrpTunnelFor(ctx, "", input)
}

// PreviewFrpTunnelFor 在编辑既有隧道时排除自身名称，避免把当前隧道判成重名。
func (r *Runtime) PreviewFrpTunnelFor(ctx context.Context, tunnelID string, input domain.FrpTunnelInput) (map[string]any, error) {
	draft, validationErrors := normalizeFrpTunnelRequest(input, r.Config.FrpProbeHost, r.Config.FrpProbePort)
	return r.previewFrpTunnelDraft(ctx, draft, tunnelID, validationErrors)
}

func (r *Runtime) previewFrpTunnelDraft(ctx context.Context, draft frpTunnelDraft, exceptTunnelID string, validationErrors []string) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	server := findObject(state.Servers, draft.ServerID)
	blocked := append([]string{}, validationErrors...)
	blocked = append(blocked, frpTunnelAssetBlockers(state, draft.Mappings)...)
	if server == nil {
		blocked = append(blocked, "SERVER_NOT_FOUND")
	} else {
		if stringValue(server["publicHost"]) == "" {
			blocked = append(blocked, "SERVER_PUBLIC_HOST_REQUIRED")
		}
		if _, ok := r.serverSSHSecretMaterial(ctx, state, server); !ok {
			blocked = append(blocked, "SERVER_SSH_SECRET_MATERIAL_REQUIRED")
		}
	}
	if duplicateTunnelName(state.FrpTunnels, draft.ServerID, draft.Name, exceptTunnelID) {
		blocked = append(blocked, "FRP_TUNNEL_NAME_DUPLICATE")
	}
	if active := domain.ActiveFrpTunnelRunForServer(state.FrpTunnelRuns, draft.ServerID, ""); active != nil {
		blocked = append(blocked, "SERVER_ALREADY_HAS_ACTIVE_FRP_TUNNEL")
	}
	readiness := []runtimeReadinessCheck{}
	if len(blocked) == 0 {
		if _, system := r.FrpExecutor.(*SystemFrpTunnelExecutor); system {
			_, err := executableFrpcPath(FrpTunnelExecutionInput{FrpcPath: r.Config.FrpcPath})
			readiness = append(readiness, runtimeReadinessCheck{
				Code: stableErrorCode(err, "FRPC_BINARY_READY"), Target: domain.Resource("frp_client", "frpc", "FRPC"),
				Passed: err == nil, Summary: "检查本机 FRPC 运行程序", Details: domain.Object{"capability": "frpc"},
			})
		}
		readiness = append(readiness, previewLocalTunnelPorts(ctx, draft.Mappings)...)
	}
	if len(blocked) == 0 && server != nil {
		readiness = append(readiness, r.previewRemoteTunnelRuntime(ctx, server, frpUsesTraefik(draft.Mappings))...)
	}
	endpoints := []string{}
	if server != nil {
		for _, mapping := range draft.Mappings {
			if publicURL := r.frpTunnelPublicURL(server, mapping); publicURL != "" {
				endpoints = append(endpoints, publicURL)
			}
		}
	}
	subject := resourceRef("server", draft.ServerID, draft.ServerID)
	if server != nil {
		subject.Name = firstNonEmptyString(stringValue(server["name"]), draft.ServerID)
	}
	builder := domain.NewCheckReportBuilder(domain.CheckKindPreview, subject)
	for _, reason := range uniqueStrings(blocked) {
		builder.Blocker(reason)
	}
	addRuntimeReadinessChecks(builder, readiness, "preview")
	if len(blocked) == 0 && runtimeReadinessAllPassed(readiness) {
		builder.Impact(domain.ResourceActionCreate, resourceRef("frp_tunnel_run", "frp_tunnel_preview", draft.Name), 1, domain.CheckSeverityInfo, "START_FRP_TUNNEL")
	}
	report := builder.Report()
	payload := map[string]any{
		"endpoints": endpoints,
		"request":   publicFrpTunnelDraft(draft),
	}
	if server != nil {
		payload["server"] = tunnelServerSummary(server)
	}
	return processResponse(report, "tunnelPreview", payload), nil
}

func previewLocalTunnelPorts(ctx context.Context, mappings []domain.FrpTunnelMapping) []runtimeReadinessCheck {
	checks := make([]runtimeReadinessCheck, 0, len(mappings))
	for _, mapping := range mappings {
		endpoint := net.JoinHostPort(mapping.LocalHost, strconv.Itoa(mapping.LocalPort))
		target := domain.Resource("frp_mapping", mapping.Name, mapping.Name)
		code := "LOCAL_TCP_PORT_UNREACHABLE:" + mapping.Name
		if err := verifyTCPPort(ctx, mapping.LocalHost, mapping.LocalPort, 2*time.Second); err != nil {
			checks = append(checks, runtimeReadinessCheck{
				Code:    code,
				Target:  target,
				Passed:  false,
				Summary: "LOCAL_TCP_PORT_UNREACHABLE:" + endpoint,
				Details: domain.Object{"endpoint": endpoint, "mapping": mapping.Name},
			})
			continue
		}
		checks = append(checks, runtimeReadinessCheck{
			Code:    code,
			Target:  target,
			Passed:  true,
			Summary: "local tcp port reachable " + endpoint,
			Details: domain.Object{"endpoint": endpoint, "mapping": mapping.Name},
		})
	}
	return checks
}

func (r *Runtime) previewRemoteTunnelRuntime(ctx context.Context, server domain.Object, useTraefik bool) []runtimeReadinessCheck {
	reply, err := r.ServerRuntimeProbe(ctx, stringValue(server["id"]))
	if err != nil {
		code := stableErrorCode(err, "SERVER_RUNTIME_PROBE_FAILED")
		return []runtimeReadinessCheck{{
			Code:    code,
			Target:  serverResourceRef(server),
			Passed:  false,
			Summary: code,
		}}
	}
	probe := objectMapFromAny(reply["serverRuntime"])
	return frpTunnelRuntimeReadiness(server, objectMapFromAny(probe["metrics"]), useTraefik)
}

// 预检使用本次服务器回读，Traefik 仅用于已绑定域名的映射。
func frpTunnelRuntimeReadiness(server domain.Object, metrics domain.Object, useTraefik bool) []runtimeReadinessCheck {
	target := serverResourceRef(server)
	swarmRole := strings.ToLower(stringValue(metrics["swarmRole"]))
	traefikCode := "TRAEFIK_GATEWAY_REQUIRED"
	if metrics["traefik"] != true && stringValue(metrics["traefikStatus"]) != "missing" {
		traefikCode = "TRAEFIK_GATEWAY_UNAVAILABLE"
	}
	frpsStatus := stringValue(metrics["frpsStatus"])
	frpsReady := frpsStatus == "installed" && metrics["frps"] == true
	frpsCode := "FRPS_INSTALLATION_UNAVAILABLE"
	switch {
	case frpsReady:
		frpsCode = "FRPS_INSTALLED"
	case frpsStatus == "missing":
		frpsCode = "FRPS_NOT_INSTALLED"
	case frpsStatus == "running":
		frpsCode = "FRPS_ALREADY_IN_USE"
	}
	checks := []runtimeReadinessCheck{
		{
			Code:    "SSH_CONNECT_FAILED",
			Target:  target,
			Passed:  metrics["ssh"] == true,
			Summary: "server SSH is reachable",
			Details: domain.Object{"capability": "ssh"},
		},
		{
			Code:    "REMOTE_DOCKER_UNAVAILABLE",
			Target:  target,
			Passed:  metrics["docker"] == true,
			Summary: "remote Docker is available",
			Details: domain.Object{"capability": "docker"},
		},
		{
			Code:    "FRP_SWARM_MANAGER_REQUIRED",
			Target:  target,
			Passed:  metrics["swarm"] == true && swarmRole == "manager",
			Summary: "服务器必须是活动的 Swarm manager",
			Details: domain.Object{"capability": "swarm", "swarmRole": swarmRole},
		},
		{
			Code: frpsCode, Target: target, Passed: frpsReady,
			Summary: "检查服务器上的受管 FRPS", Details: domain.Object{"capability": "frps", "frpsStatus": frpsStatus},
		},
	}
	if useTraefik {
		checks = append(checks, runtimeReadinessCheck{
			Code: traefikCode, Target: target, Passed: metrics["traefik"] == true,
			Summary: "检查域名映射所需的 Traefik 网关", Details: domain.Object{"capability": "traefik", "traefikStatus": stringValue(metrics["traefikStatus"])},
		})
	}
	return checks
}

func (r *Runtime) CreateFrpTunnel(ctx context.Context, input domain.FrpTunnelInput) (map[string]any, error) {
	draft, validationErrors := normalizeFrpTunnelRequest(input, r.Config.FrpProbeHost, r.Config.FrpProbePort)
	preview, err := r.previewFrpTunnelDraft(ctx, draft, "", validationErrors)
	if err != nil {
		return nil, err
	}
	if !processDecisionAllowed(preview["process"]) {
		return nil, errors.New("FRP_TUNNEL_PREVIEW_BLOCKED:" + strings.Join(processBlockers(preview["process"]), ","))
	}
	now := domain.Now()
	tunnel := domain.FrpTunnel{
		ID:           domain.NewID("tun"),
		Name:         draft.Name,
		ServerID:     draft.ServerID,
		FrpsBindPort: draft.FrpsBindPort,
		Mappings:     cloneFrpTunnelMappings(draft.Mappings),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	run, task, err := r.createFrpTunnelRun(ctx, &tunnel, true)
	if err != nil {
		return nil, err
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"tunnel": r.publicFrpTunnel(tunnel, state, 0),
		"run":    publicFrpTunnelRun(run),
		"task":   domain.TaskResponse(task),
	}, nil
}

func (r *Runtime) StartFrpTunnel(ctx context.Context, tunnelID string) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	tunnel := domain.FindFrpTunnel(state.FrpTunnels, tunnelID)
	if tunnel == nil {
		return nil, errors.New("FRP_TUNNEL_NOT_FOUND")
	}
	draft := frpTunnelDraft{Name: tunnel.Name, ServerID: tunnel.ServerID, FrpsBindPort: tunnel.FrpsBindPort, Mappings: cloneFrpTunnelMappings(tunnel.Mappings)}
	preview, err := r.previewFrpTunnelDraft(ctx, draft, tunnel.ID, nil)
	if err != nil {
		return nil, err
	}
	if !processDecisionAllowed(preview["process"]) {
		return nil, errors.New("FRP_TUNNEL_PREVIEW_BLOCKED:" + strings.Join(processBlockers(preview["process"]), ","))
	}
	run, task, err := r.createFrpTunnelRun(ctx, tunnel, false)
	if err != nil {
		return nil, err
	}
	state, err = r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	hitCount, err := r.Store.FrpProbeHitCount(ctx, tunnel.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"tunnel": r.publicFrpTunnel(*tunnel, state, hitCount),
		"run":    publicFrpTunnelRun(run),
		"task":   domain.TaskResponse(task),
	}, nil
}

func (r *Runtime) createFrpTunnelRun(ctx context.Context, tunnel *domain.FrpTunnel, createTunnel bool) (domain.FrpTunnelRun, domain.Task, error) {
	if tunnel == nil {
		return domain.FrpTunnelRun{}, domain.Task{}, errors.New("FRP_TUNNEL_NOT_FOUND")
	}
	now := domain.Now()
	runID := domain.NewID("tunrun")
	tokenSecretRef := "secret_" + domain.NewID("frp")
	run := domain.FrpTunnelRun{
		ID:             runID,
		TunnelID:       tunnel.ID,
		ServerID:       tunnel.ServerID,
		Status:         frpTunnelStatusPendingConfirmation,
		FrpsBindPort:   tunnel.FrpsBindPort,
		FrpsImage:      r.Config.FrpsImage,
		FrpVersion:     r.Config.FrpVersion,
		Mappings:       cloneFrpTunnelMappings(tunnel.Mappings),
		TokenSecretRef: tokenSecretRef,
		ContainerName:  frpTunnelContainerName(runID),
		RemoteDir:      frpTunnelRemoteDir(runID),
		ManagedFrps:    true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	task := newTask(string(domain.TaskActionStartFrpTunnel), "frp_tunnel_run", run.ID, tunnel.Name, map[string]any{
		"tunnelId":     tunnel.ID,
		"serverId":     tunnel.ServerID,
		"mappingCount": len(tunnel.Mappings),
	})
	run.StartOperationRunID = task.ID
	err := r.Store.MutateStateWithSecretMaterials(ctx, map[string]string{tokenSecretRef: domain.NewID("frp_token")}, func(state *domain.State) error {
		if reused, err := reuseTaskRequest(ctx, *state, &task); reused || err != nil {
			if existing := domain.FindFrpTunnelRun(state.FrpTunnelRuns, task.TargetID); existing != nil {
				run = *existing
				if saved := domain.FindFrpTunnel(state.FrpTunnels, run.TunnelID); saved != nil {
					*tunnel = *saved
				}
			}
			return err
		}
		if findObject(state.Servers, tunnel.ServerID) == nil {
			return errors.New("SERVER_NOT_FOUND")
		}
		if reasons := frpTunnelAssetBlockers(*state, tunnel.Mappings); len(reasons) > 0 {
			return errors.New("FRP_TUNNEL_PREVIEW_BLOCKED:" + strings.Join(reasons, ","))
		}
		if createTunnel {
			if domain.FindFrpTunnel(state.FrpTunnels, tunnel.ID) != nil {
				return errors.New("FRP_TUNNEL_ALREADY_EXISTS")
			}
			if duplicateTunnelName(state.FrpTunnels, tunnel.ServerID, tunnel.Name, "") {
				return errors.New("FRP_TUNNEL_NAME_DUPLICATE")
			}
			state.FrpTunnels = append(state.FrpTunnels, *tunnel)
		} else {
			current := domain.FindFrpTunnel(state.FrpTunnels, tunnel.ID)
			if current == nil {
				return errors.New("FRP_TUNNEL_NOT_FOUND")
			}
			if current.UpdatedAt != tunnel.UpdatedAt {
				return errors.New("FRP_TUNNEL_CHANGED")
			}
		}
		if domain.ActiveFrpTunnelRunForServer(state.FrpTunnelRuns, tunnel.ServerID, "") != nil {
			return errors.New("SERVER_ALREADY_HAS_ACTIVE_FRP_TUNNEL")
		}
		state.FrpTunnelRuns = append(state.FrpTunnelRuns, run)
		return enqueueTaskRequest(ctx, state, &task)
	})
	return run, task, err
}

func (r *Runtime) UpdateFrpTunnel(ctx context.Context, id string, input domain.FrpTunnelInput) (domain.Object, error) {
	draft, validationErrors := normalizeFrpTunnelRequest(input, r.Config.FrpProbeHost, r.Config.FrpProbePort)
	if len(validationErrors) > 0 {
		return nil, errors.New("FRP_TUNNEL_INVALID:" + strings.Join(validationErrors, ","))
	}
	var updated domain.FrpTunnel
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		if reasons := frpTunnelAssetBlockers(*state, draft.Mappings); len(reasons) > 0 {
			return errors.New("FRP_TUNNEL_INVALID:" + strings.Join(reasons, ","))
		}
		if frpTunnelHasBlockingRun(state.FrpTunnelRuns, id) {
			return errors.New("FRP_TUNNEL_NOT_EDITABLE")
		}
		if findObject(state.Servers, draft.ServerID) == nil {
			return errors.New("SERVER_NOT_FOUND")
		}
		if duplicateTunnelName(state.FrpTunnels, draft.ServerID, draft.Name, id) {
			return errors.New("FRP_TUNNEL_NAME_DUPLICATE")
		}
		for index := range state.FrpTunnels {
			if state.FrpTunnels[index].ID != id {
				continue
			}
			state.FrpTunnels[index].Name = draft.Name
			state.FrpTunnels[index].ServerID = draft.ServerID
			state.FrpTunnels[index].FrpsBindPort = draft.FrpsBindPort
			state.FrpTunnels[index].Mappings = cloneFrpTunnelMappings(draft.Mappings)
			state.FrpTunnels[index].UpdatedAt = domain.Now()
			updated = state.FrpTunnels[index]
			state.OperationLogs = append(state.OperationLogs, newOperationLog("update_frp_tunnel", "frp_tunnel", id, "succeeded", "已更新穿透配置", ""))
			return nil
		}
		return errors.New("FRP_TUNNEL_NOT_FOUND")
	})
	if err != nil {
		return nil, err
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	hitCount, err := r.Store.FrpProbeHitCount(ctx, id)
	if err != nil {
		return nil, err
	}
	return r.publicFrpTunnel(updated, state, hitCount), nil
}

func (r *Runtime) StopFrpTunnel(ctx context.Context, tunnelID string) (domain.Task, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	tunnel := domain.FindFrpTunnel(state.FrpTunnels, tunnelID)
	if tunnel == nil {
		return domain.Task{}, errors.New("FRP_TUNNEL_NOT_FOUND")
	}
	run := latestActiveFrpTunnelRun(state.FrpTunnelRuns, tunnelID)
	if run == nil || !run.Status.NeedsCleanup() || run.Status == frpTunnelStatusStopping {
		return domain.Task{}, errors.New("FRP_TUNNEL_NOT_STOPPABLE")
	}
	task := newTask(string(domain.TaskActionStopFrpTunnel), "frp_tunnel_run", run.ID, tunnel.Name, map[string]any{
		"tunnelId": tunnel.ID,
		"serverId": tunnel.ServerID,
	})
	err = r.Store.MutateState(ctx, func(state *domain.State) error {
		if reused, err := reuseTaskRequest(ctx, *state, &task); reused || err != nil {
			return err
		}
		current := domain.FindFrpTunnelRun(state.FrpTunnelRuns, run.ID)
		if current == nil || !current.Status.NeedsCleanup() || current.Status == frpTunnelStatusStopping {
			return errors.New("FRP_TUNNEL_NOT_STOPPABLE")
		}
		return enqueueTaskRequest(ctx, state, &task)
	})
	return task, err
}

func (r *Runtime) DeleteFrpTunnel(ctx context.Context, tunnelID string) (domain.Task, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	tunnel := domain.FindFrpTunnel(state.FrpTunnels, tunnelID)
	if tunnel == nil {
		return domain.Task{}, errors.New("FRP_TUNNEL_NOT_FOUND")
	}
	if frpTunnelHasBlockingRun(state.FrpTunnelRuns, tunnelID) {
		return domain.Task{}, errors.New("FRP_TUNNEL_NOT_DELETABLE")
	}
	return r.CreateTask(ctx, string(domain.TaskActionDeleteFrpTunnel), "frp_tunnel", tunnelID, tunnel.Name, map[string]any{"serverId": tunnel.ServerID})
}

func (r *Runtime) serverSSHSecretMaterial(ctx context.Context, state domain.State, server domain.Object) (string, bool) {
	for _, assetID := range objectStringSlice(server["secretAssetIds"]) {
		asset := findObject(state.SecretAssets, assetID)
		if asset == nil || stringValue(asset["assetType"]) != "ssh_key" || stringValue(asset["purpose"]) != "deployment_server" {
			continue
		}
		material, err := r.Store.GetSecretMaterial(ctx, stringValue(asset["secretRef"]))
		if err == nil && material != "" {
			return material, true
		}
	}
	return "", false
}
