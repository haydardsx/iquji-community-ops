package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const maintenanceChallengeTTL = 5 * time.Minute

type maintenanceChallenge struct {
	Action      domain.DataMaintenanceAction
	StateDigest string
}

type quarantinedPath struct {
	original string
	target   string
}

func (r *Runtime) PreviewDataMaintenance(ctx context.Context, action domain.DataMaintenanceAction) (domain.DataMaintenancePreview, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return domain.DataMaintenancePreview{}, err
	}
	preview, err := r.runtimeDataMaintenancePreview(ctx, action, state)
	if err != nil {
		return domain.DataMaintenancePreview{}, err
	}
	preview.ConfirmationToken = domain.NewID("maintenance")
	preview.ExpiresAt = time.Now().UTC().Add(maintenanceChallengeTTL).Format(time.RFC3339Nano)
	r.Cache.Set(maintenanceChallengeKey(preview.ConfirmationToken), maintenanceChallenge{
		Action:      action,
		StateDigest: preview.StateDigest,
	}, maintenanceChallengeTTL)
	return preview, nil
}

func (r *Runtime) ExecuteDataMaintenance(ctx context.Context, action domain.DataMaintenanceAction, input domain.DataMaintenanceExecuteRequest) (domain.DataMaintenanceResult, error) {
	value, ok := r.Cache.Take(maintenanceChallengeKey(strings.TrimSpace(input.ConfirmationToken)))
	if !ok {
		return domain.DataMaintenanceResult{}, errors.New("DATA_MAINTENANCE_CONFIRMATION_INVALID")
	}
	challenge, ok := value.(maintenanceChallenge)
	if !ok || challenge.Action != action || challenge.StateDigest != input.StateDigest {
		return domain.DataMaintenanceResult{}, errors.New("DATA_MAINTENANCE_CONFIRMATION_INVALID")
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return domain.DataMaintenanceResult{}, err
	}
	preview, err := r.runtimeDataMaintenancePreview(ctx, action, state)
	if err != nil {
		return domain.DataMaintenanceResult{}, err
	}
	if preview.StateDigest != input.StateDigest {
		return domain.DataMaintenanceResult{}, errors.New("DATA_MAINTENANCE_STATE_CHANGED")
	}
	if input.ConfirmationPhrase != preview.ConfirmationPhrase {
		return domain.DataMaintenanceResult{}, errors.New("DATA_MAINTENANCE_PHRASE_INVALID")
	}
	if len(preview.Blockers) > 0 {
		return domain.DataMaintenanceResult{}, errors.New(preview.Blockers[0])
	}
	ctx = sqlite.WithMaintenanceGuard(ctx, maintenanceStateDigest(state))

	paths := []string{"restore-staging", "backup-staging"}
	if action == domain.DataMaintenanceFactoryReset {
		paths = []string{"uploads", "frp-tunnels", "restore-staging", "backup-staging"}
	}
	quarantine, err := quarantineStatePaths(r.Config.StateDir, paths)
	if err != nil {
		return domain.DataMaintenanceResult{}, err
	}
	restoreQuarantine := true
	defer func() {
		if restoreQuarantine {
			restoreQuarantinedPaths(quarantine)
		}
	}()

	switch action {
	case domain.DataMaintenanceClearLogs:
		err = r.Store.ClearOperationalHistory(ctx)
	case domain.DataMaintenanceFactoryReset:
		fresh := domain.EmptyState()
		domain.EnsureBuiltinProject(&fresh)
		err = r.Store.FactoryReset(ctx, fresh)
	default:
		err = errors.New("DATA_MAINTENANCE_ACTION_INVALID")
	}
	if err != nil {
		return domain.DataMaintenanceResult{}, err
	}
	// The database transaction is committed at this point. Quarantined paths must
	// never be restored after commit, otherwise cleared files would reappear next
	// to an already-cleared database. A final deletion error is surfaced while
	// the data remains isolated under its quarantine name.
	restoreQuarantine = false
	r.Cache.Clear()
	if err := removeQuarantinedPaths(quarantine); err != nil {
		return domain.DataMaintenanceResult{}, err
	}
	return domain.DataMaintenanceResult{
		Action:        action,
		Status:        "succeeded",
		ClearedCounts: preview.DeleteCounts,
		CompletedAt:   domain.Now(),
	}, nil
}

func dataMaintenancePreview(action domain.DataMaintenanceAction, state domain.State) (domain.DataMaintenancePreview, error) {
	if action != domain.DataMaintenanceClearLogs && action != domain.DataMaintenanceFactoryReset {
		return domain.DataMaintenancePreview{}, errors.New("DATA_MAINTENANCE_ACTION_INVALID")
	}
	deleteCounts := map[string]int{
		"runtimeLogs":   len(state.RuntimeLogs),
		"operationLogs": len(state.OperationLogs),
	}
	preserveCounts := map[string]int{
		"projects":                     len(state.Projects),
		"applications":                 len(state.Applications),
		"applicationDeploymentConfigs": len(state.ApplicationDeploymentConfigs),
		"applicationTemplates":         len(state.ApplicationTemplates),
		"applicationInstances":         len(state.ApplicationInstances),
		"routes":                       len(state.Routes),
		"servers":                      len(state.Servers),
		"serverRoles":                  len(state.ServerRoles),
		"secretAssets":                 len(state.SecretAssets),
		"domains":                      len(state.Domains),
		"certificates":                 len(state.Certificates),
		"resolvers":                    len(state.Resolvers),
		"ingresses":                    len(state.Ingresses),
		"releasePlans":                 len(state.ReleasePlans),
		"monitoringAgents":             len(state.MonitoringAgents),
		"runtimeLogStreams":            len(state.RuntimeLogStreams),
		"frpTunnels":                   len(state.FrpTunnels),
	}
	if action == domain.DataMaintenanceFactoryReset {
		for key, value := range preserveCounts {
			deleteCounts[key] = value
		}
		preserveCounts = map[string]int{}
	}
	blockers := maintenanceBlockers(state)
	phrase := "清空日志"
	if action == domain.DataMaintenanceFactoryReset {
		phrase = "清空全部数据"
	}
	return domain.DataMaintenancePreview{
		Action:             action,
		ConfirmationPhrase: phrase,
		StateDigest:        maintenanceStateDigest(state),
		DeleteCounts:       deleteCounts,
		PreserveCounts:     preserveCounts,
		Blockers:           blockers,
		Warnings: []string{
			"仅清理本机控制面数据，不会卸载或删除远端 Docker、Swarm、Traefik、Jenkins、环境或应用资源。",
		},
	}, nil
}

func (r *Runtime) runtimeDataMaintenancePreview(ctx context.Context, action domain.DataMaintenanceAction, state domain.State) (domain.DataMaintenancePreview, error) {
	preview, err := dataMaintenancePreview(action, state)
	if err != nil {
		return domain.DataMaintenancePreview{}, err
	}
	sideCounts, err := r.Store.DataMaintenanceCounts(ctx)
	if err != nil {
		return domain.DataMaintenancePreview{}, err
	}
	for key, count := range sideCounts {
		if action == domain.DataMaintenanceClearLogs && (key == "secretMaterials" || key == "monitoringAgentRegistrations") {
			preview.PreserveCounts[key] = count
			continue
		}
		if count > preview.DeleteCounts[key] {
			preview.DeleteCounts[key] = count
		}
	}
	fileCounts, err := dataMaintenanceFileCounts(r.Config.StateDir, action)
	if err != nil {
		return domain.DataMaintenancePreview{}, err
	}
	for key, count := range fileCounts {
		preview.DeleteCounts[key] = count
	}
	if action == domain.DataMaintenanceClearLogs {
		preservedFileCounts, countErr := dataMaintenancePreservedFileCounts(r.Config.StateDir)
		if countErr != nil {
			return domain.DataMaintenancePreview{}, countErr
		}
		for key, count := range preservedFileCounts {
			preview.PreserveCounts[key] = count
		}
	} else {
		preview.PreserveCounts["projects"] = 1
		if preview.DeleteCounts["projects"] > 0 {
			preview.DeleteCounts["projects"]--
		}
	}
	digestInput := struct {
		Action         domain.DataMaintenanceAction `json:"action"`
		State          string                       `json:"state"`
		SideCounts     map[string]int               `json:"sideCounts"`
		FileCounts     map[string]int               `json:"fileCounts"`
		PreserveCounts map[string]int               `json:"preserveCounts"`
	}{
		Action: action, State: maintenanceStateDigest(state), SideCounts: sideCounts,
		FileCounts: fileCounts, PreserveCounts: preview.PreserveCounts,
	}
	data, err := json.Marshal(digestInput)
	if err != nil {
		return domain.DataMaintenancePreview{}, err
	}
	hash := sha256.Sum256(data)
	preview.StateDigest = hex.EncodeToString(hash[:])
	return preview, nil
}

func maintenanceBlockers(state domain.State) []string {
	blockers := []string{}
	for _, task := range state.Tasks {
		if domain.IsTaskActive(task.Status) {
			blockers = append(blockers, "DATA_MAINTENANCE_ACTIVE_TASK")
			break
		}
	}
	for _, run := range state.JenkinsReleaseRuns {
		switch stringValue(run["status"]) {
		case "pending", "triggered", "callback_received", "cleanup_required":
			blockers = append(blockers, "DATA_MAINTENANCE_ACTIVE_JENKINS_RUN")
		}
	}
	for _, run := range state.FrpTunnelRuns {
		if run.Status.IsActive() {
			blockers = append(blockers, "DATA_MAINTENANCE_ACTIVE_FRP_TUNNEL")
			break
		}
	}
	sort.Strings(blockers)
	return uniqueStrings(blockers)
}

func maintenanceStateDigest(state domain.State) string {
	data, _ := json.Marshal(state)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func countTerminalFrpRuns(runs []domain.FrpTunnelRun) int {
	count := 0
	for _, run := range runs {
		if !run.Status.IsActive() {
			count++
		}
	}
	return count
}

func maintenanceChallengeKey(token string) string {
	return "data-maintenance:" + token
}

func dataMaintenanceFileCounts(stateDir string, action domain.DataMaintenanceAction) (map[string]int, error) {
	paths := map[string]string{
		"restoreStagingFiles": "restore-staging",
		"backupStagingFiles":  "backup-staging",
	}
	if action == domain.DataMaintenanceFactoryReset {
		paths["uploadAssets"] = "uploads"
		paths["frpRuntimeFiles"] = "frp-tunnels"
	}
	result := make(map[string]int, len(paths))
	for key, name := range paths {
		count, err := countStateFiles(filepath.Join(stateDir, name))
		if err != nil {
			return nil, err
		}
		result[key] = count
	}
	return result, nil
}

func dataMaintenancePreservedFileCounts(stateDir string) (map[string]int, error) {
	result := map[string]int{}
	for key, name := range map[string]string{
		"uploadAssets":    "uploads",
		"frpRuntimeFiles": "frp-tunnels",
	} {
		count, err := countStateFiles(filepath.Join(stateDir, name))
		if err != nil {
			return nil, err
		}
		result[key] = count
	}
	return result, nil
}

func countStateFiles(root string) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry != nil && !entry.IsDir() {
			count++
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	return count, err
}

func quarantineStatePaths(stateDir string, names []string) ([]quarantinedPath, error) {
	items := []quarantinedPath{}
	for _, name := range names {
		original := filepath.Join(stateDir, name)
		if _, err := os.Stat(original); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			restoreQuarantinedPaths(items)
			return nil, err
		}
		target := filepath.Join(stateDir, ".maintenance-quarantine-"+domain.NewID("dir")+"-"+name)
		if err := os.Rename(original, target); err != nil {
			restoreQuarantinedPaths(items)
			return nil, err
		}
		items = append(items, quarantinedPath{original: original, target: target})
	}
	return items, nil
}

func restoreQuarantinedPaths(items []quarantinedPath) {
	for index := len(items) - 1; index >= 0; index-- {
		_ = os.Rename(items[index].target, items[index].original)
	}
}

func removeQuarantinedPaths(items []quarantinedPath) error {
	errorsFound := []error{}
	for _, item := range items {
		if err := os.RemoveAll(item.target); err != nil {
			errorsFound = append(errorsFound, err)
		}
	}
	return errors.Join(errorsFound...)
}
