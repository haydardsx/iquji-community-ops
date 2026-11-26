package application

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

func TestDataMaintenanceClearLogsPreservesBusinessAndLeavesNoReceipt(t *testing.T) {
	runtime, store, ctx := newDataMaintenanceRuntime(t)
	defer func() { _ = store.Close() }()
	if err := store.MutateStateWithSecretMaterials(ctx, map[string]string{"secret_1": "private-key"}, func(state *domain.State) error {
		state.Projects = []domain.Object{{"id": "project_1", "name": "Project"}}
		state.Servers = []domain.Object{{"id": "server_1", "name": "Server"}}
		state.SecretAssets = []domain.Object{{"id": "asset_1", "secretRef": "secret_1"}}
		state.Tasks = []domain.Task{{ID: "task_old", Action: "deploy_application", Status: "succeeded", TargetKind: "application", TargetID: "app_1", Payload: map[string]any{}, CreatedAt: domain.Now(), UpdatedAt: domain.Now()}}
		state.RuntimeLogs = []domain.RuntimeLog{{ID: "log_old", OperationRunID: "operation_old", Source: "worker", Phase: "complete", Level: "info", Message: "done", Timestamp: domain.Now()}}
		state.OperationLogs = []domain.OperationLog{{ID: "operation_old", OperationRunID: "operation_old", Action: "deploy_application", Status: "succeeded", Timestamp: domain.Now()}}
		return nil
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	for _, name := range []string{"restore-staging", "backup-staging"} {
		stagingDir := filepath.Join(runtime.Config.StateDir, name)
		if err := os.MkdirAll(stagingDir, 0o700); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(stagingDir, "old.backup"), []byte("old"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	preview, err := runtime.PreviewDataMaintenance(ctx, domain.DataMaintenanceClearLogs)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(preview.Blockers) != 0 || preview.DeleteCounts["operationLogs"] != 1 {
		t.Fatal("unexpected preview")
	}
	for key, expected := range map[string]int{
		"projects": 2, "servers": 1, "secretAssets": 1, "secretMaterials": 1,
		"restoreStagingFiles": 1, "backupStagingFiles": 1,
	} {
		counts := preview.PreserveCounts
		if key == "restoreStagingFiles" || key == "backupStagingFiles" {
			counts = preview.DeleteCounts
		}
		if counts[key] != expected {
			t.Fatalf("preview count %s = %d, want %d", key, counts[key], expected)
		}
	}
	result, err := runtime.ExecuteDataMaintenance(ctx, domain.DataMaintenanceClearLogs, domain.DataMaintenanceExecuteRequest{
		ConfirmationToken: preview.ConfirmationToken, ConfirmationPhrase: preview.ConfirmationPhrase, StateDigest: preview.StateDigest,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Status != "succeeded" {
		t.Fatal("unexpected result")
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(state.Projects) != 2 || len(state.Servers) != 1 || len(state.SecretAssets) != 1 {
		t.Fatal("business state changed")
	}
	if len(state.Tasks) != 0 || len(state.RuntimeLogs) != 0 || len(state.OperationLogs) != 0 {
		t.Fatal("history or self receipt remains")
	}
	if material, err := store.GetSecretMaterial(ctx, "secret_1"); err != nil || material != "private-key" {
		t.Fatal("secret was not preserved")
	}
	for _, name := range []string{"restore-staging", "backup-staging"} {
		if _, err := os.Stat(filepath.Join(runtime.Config.StateDir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s remains: %v", name, err)
		}
	}
}

func TestDataMaintenanceFactoryResetReturnsFreshInstallBaseline(t *testing.T) {
	runtime, store, ctx := newDataMaintenanceRuntime(t)
	defer func() { _ = store.Close() }()
	if err := store.MutateStateWithSecretMaterials(ctx, map[string]string{"secret_1": "private-key"}, func(state *domain.State) error {
		state.Projects = []domain.Object{{"id": "project_1"}}
		state.ApplicationTemplates = []domain.Object{{"id": "custom-template"}}
		state.FrpTunnels = []domain.FrpTunnel{{ID: "tunnel_1", Name: "Stopped", ServerID: "server_1"}}
		return nil
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	for _, name := range []string{"uploads", "frp-tunnels", "restore-staging", "backup-staging"} {
		dir := filepath.Join(runtime.Config.StateDir, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "data"), []byte("data"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	preview, err := runtime.PreviewDataMaintenance(ctx, domain.DataMaintenanceFactoryReset)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.DeleteCounts["projects"] != 1 || preview.DeleteCounts["frpTunnels"] != 1 || preview.DeleteCounts["secretMaterials"] != 1 {
		t.Fatal("factory reset delete counts are incomplete")
	}
	if preview.PreserveCounts["projects"] != 1 {
		t.Fatal("缺少内置项目基线")
	}
	if _, err := runtime.ExecuteDataMaintenance(ctx, domain.DataMaintenanceFactoryReset, domain.DataMaintenanceExecuteRequest{
		ConfirmationToken: preview.ConfirmationToken, ConfirmationPhrase: preview.ConfirmationPhrase, StateDigest: preview.StateDigest,
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load fresh state: %v", err)
	}
	if len(state.Projects) != 1 || state.Projects[0]["id"] != domain.BuiltinProjectID || len(state.FrpTunnels) != 0 || len(state.Tasks) != 0 || len(state.OperationLogs) != 0 {
		t.Fatal("user state remains")
	}
	if !slices.EqualFunc(state.ApplicationTemplates, domain.DefaultApplicationTemplates(), templateContentsEqual) {
		t.Fatal("恢复出厂后未还原初始模板目录")
	}
	if _, err := store.GetSecretMaterial(ctx, "secret_1"); err == nil {
		t.Fatal("secret survived factory reset")
	}
	for _, name := range []string{"uploads", "frp-tunnels", "restore-staging", "backup-staging"} {
		if _, err := os.Stat(filepath.Join(runtime.Config.StateDir, name)); !os.IsNotExist(err) {
			t.Fatalf("state directory %s remains: %v", name, err)
		}
	}
}

func TestDataMaintenanceRejectsActiveWorkAndConsumesChallenge(t *testing.T) {
	runtime, store, ctx := newDataMaintenanceRuntime(t)
	defer func() { _ = store.Close() }()
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Tasks = []domain.Task{{ID: "task_active", Action: "deploy_application", Status: "executing", TargetKind: "application", TargetID: "app_1", Payload: map[string]any{}, CreatedAt: domain.Now(), UpdatedAt: domain.Now()}}
		return nil
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	preview, err := runtime.PreviewDataMaintenance(ctx, domain.DataMaintenanceClearLogs)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(preview.Blockers) == 0 || preview.Blockers[0] != "DATA_MAINTENANCE_ACTIVE_TASK" {
		t.Fatal("active task was not blocked")
	}
	input := domain.DataMaintenanceExecuteRequest{ConfirmationToken: preview.ConfirmationToken, ConfirmationPhrase: preview.ConfirmationPhrase, StateDigest: preview.StateDigest}
	if _, err := runtime.ExecuteDataMaintenance(ctx, domain.DataMaintenanceClearLogs, input); err == nil || err.Error() != "DATA_MAINTENANCE_ACTIVE_TASK" {
		t.Fatalf("execute error = %v", err)
	}
	if _, err := runtime.ExecuteDataMaintenance(ctx, domain.DataMaintenanceClearLogs, input); err == nil || err.Error() != "DATA_MAINTENANCE_CONFIRMATION_INVALID" {
		t.Fatalf("challenge was reusable: %v", err)
	}
}

func newDataMaintenanceRuntime(t *testing.T) (*Runtime, *sqlite.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	stateDir := t.TempDir()
	config := platform.Resolve(platform.RoleAPI)
	config.RepositoryRoot = filepath.Join("..", "..", "..")
	config.StateDir = stateDir
	config.DatabasePath = filepath.Join(stateDir, "state.sqlite")
	store, err := sqlite.Open(ctx, config.DatabasePath, config.RepositoryRoot)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return NewRuntime(config, store), store, ctx
}
