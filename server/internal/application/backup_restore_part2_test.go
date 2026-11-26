package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

func TestBackupAssetIDNamesOneLocalFile(t *testing.T) {
	for _, name := range []string{"upload_example", "image.1.png", "data-2"} {
		if !validBackupAssetID(name) {
			t.Fatal("拒绝了有效的备份文件名")
		}
	}
	for _, name := range []string{"", ".", "..", "/asset", "a/b", `a\b`, "a:b", "a\x00b"} {
		if validBackupAssetID(name) {
			t.Fatal("备份资产必须是单独的本地文件名")
		}
	}
}

func TestBackupOperationHistoryContainsOnlyOperationAndRuntimeLogs(t *testing.T) {
	runtime, store, ctx := newBackupRestoreRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Projects = append(state.Projects, domain.Object{"id": "project_history", "name": "History"})
		state.Applications = append(state.Applications, domain.Object{"id": "application_history", "projectId": "project_history", "name": "App"})
		state.OperationRuns = append(state.OperationRuns, domain.OperationRun{ID: "operation_history", Action: "request_jenkins_release", Status: "succeeded", TargetKind: "application", TargetID: "application_history", CreatedAt: domain.Now(), UpdatedAt: domain.Now()})
		state.OperationLogs = append(state.OperationLogs, domain.OperationLog{ID: "operation_log_history", OperationRunID: "operation_history", Action: "request_jenkins_release", Status: "succeeded", TargetKind: "application", TargetID: "application_history", Level: "info", Message: "done", Timestamp: domain.Now()})
		state.RuntimeLogs = append(state.RuntimeLogs, domain.RuntimeLog{ID: "runtime_log_history", OperationRunID: "operation_history", Source: "jenkins", Phase: "pipeline", Level: "info", Message: "done", Timestamp: domain.Now(), TargetKind: "application", TargetID: "application_history"})
		return nil
	}); err != nil {
		t.Fatalf("seed operation history state: %v", err)
	}
	service, err := runtime.backupArchiveService()
	if err != nil {
		t.Fatalf("create history backup service: %v", err)
	}
	packageData, err := service.BuildPackage(ctx, domain.BackupOptions{
		RequestedScopes:     []domain.BackupScope{domain.BackupScopeProjects},
		IncludeLogsAndAudit: true,
	})
	if err != nil {
		t.Fatalf("build history package: %v", err)
	}
	if packageData.Payload.LogsAndAudit == nil || len(packageData.Payload.LogsAndAudit.OperationLogs) != 1 || len(packageData.Payload.LogsAndAudit.RuntimeLogs) != 1 {
		t.Fatal("operation and runtime logs missing from package")
	}
	encoded, err := json.Marshal(packageData.Payload.LogsAndAudit)
	if err != nil {
		t.Fatalf("encode operation history: %v", err)
	}
	for _, removed := range []string{"tasks", "taskStepLogs", "auditLogs", "processReports", "buildLogs", "jenkinsReleaseRuns"} {
		if bytes.Contains(encoded, []byte(`"`+removed+`"`)) {
			t.Fatal("backup operation history retained a restricted field")
		}
	}
}

func TestPreparedRestoreFilesRollbackRestoresPreviousAsset(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "uploads", "asset")
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatalf("mkdir uploads: %v", err)
	}
	if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
		t.Fatalf("write old asset: %v", err)
	}
	transactionRoot := filepath.Join(root, "transaction")
	if err := os.MkdirAll(transactionRoot, 0o700); err != nil {
		t.Fatalf("mkdir transaction: %v", err)
	}
	staged := filepath.Join(transactionRoot, "staged")
	if err := os.WriteFile(staged, []byte("new"), 0o600); err != nil {
		t.Fatalf("write staged asset: %v", err)
	}
	prepared := &preparedRestoreFiles{root: transactionRoot, items: []preparedRestoreFile{{
		stagedPath: staged, destinationPath: destination, rollbackPath: filepath.Join(transactionRoot, "rollback"),
	}}}
	if err := prepared.install(); err != nil {
		t.Fatalf("install prepared asset: %v", err)
	}
	prepared.rollback()
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "old" {
		t.Fatal("rollback did not restore previous asset")
	}
}

func TestApplicationSourceConfigRejectsUploadSources(t *testing.T) {
	runtime, store, ctx := newBackupRestoreRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Projects = append(state.Projects, domain.Object{"id": "project_upload", "name": "Upload", "slug": "upload", "applicationIds": []string{"app_upload"}, "serverIds": []string{}})
		state.Applications = append(state.Applications, domain.Object{"id": "app_upload", "projectId": "project_upload", "name": "Upload app", "slug": "upload-app"})
		return nil
	}); err != nil {
		t.Fatalf("seed upload application: %v", err)
	}
	_, err := runtime.UpsertApplicationConfig(ctx, "app_upload", domain.Object{"sourceConfig": domain.Object{
		"provider": "upload", "artifactFileName": "source.tar.gz", "sourcePath": ".",
	}})
	if err == nil || err.Error() != "APPLICATION_SOURCE_PROVIDER_INVALID" {
		t.Fatalf("upload source must be rejected: %v", err)
	}
}

func newBackupRestoreRuntime(t *testing.T) (*Runtime, *sqlite.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	stateDir := t.TempDir()
	repositoryRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("repository root: %v", err)
	}
	config := platform.Resolve(platform.RoleAPI)
	config.RepositoryRoot = repositoryRoot
	config.StateDir = stateDir
	config.DatabasePath = filepath.Join(stateDir, "state.sqlite")
	store, err := sqlite.Open(ctx, config.DatabasePath, config.RepositoryRoot)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewRuntime(config, store), store, ctx
}

func seedBackupRestoreSource(t *testing.T, store *sqlite.Store, ctx context.Context, includeMaterial bool) {
	t.Helper()
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Projects = append(state.Projects, domain.Object{"id": "project_source", "name": "Source", "slug": "source", "applicationIds": []string{"app_source"}, "serverIds": []string{"server_source"}})
		state.Applications = append(state.Applications, domain.Object{
			"id": "app_source", "projectId": "project_source", "name": "API", "slug": "api",
			"sourceConfig": domain.Object{"provider": "git", "repositoryUrl": "https://example.test/source.git", "authSecretRef": "secret_source"},
		})
		state.Servers = append(state.Servers, domain.Object{"id": "server_source", "name": "Server", "publicHost": "203.0.113.10", "secretAssetIds": []string{"asset_source"}})
		state.SecretAssets = append(state.SecretAssets, domain.Object{"id": "asset_source", "assetType": "ssh_key", "name": "Deploy key", "secretRef": "secret_source"})
		return nil
	}); err != nil {
		t.Fatalf("seed source state: %v", err)
	}
	if includeMaterial {
		if err := store.UpsertSecretMaterial(ctx, "secret_source", "synthetic-backup-value"); err != nil {
			t.Fatalf("seed source material: %v", err)
		}
	}
}

func resetStoreToFreshState(t *testing.T, store *sqlite.Store, ctx context.Context) {
	t.Helper()
	fresh := domain.EmptyState()
	if err := store.FactoryReset(ctx, fresh); err != nil {
		t.Fatalf("factory reset store: %v", err)
	}
}
