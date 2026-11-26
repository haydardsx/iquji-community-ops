package application

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSensitiveBackupRestoreRoundTripUsesRealSecretMaterial(t *testing.T) {
	runtime, store, ctx := newBackupRestoreRuntime(t)
	seedBackupRestoreSource(t, store, ctx, true)
	registry := seedSecretAsset(t, ctx, runtime, domain.Object{"assetType": "registry_secret", "name": "示例镜像仓库", "secretValue": "example-publish"})
	cfg := domain.Object{"id": "config_source", "applicationId": "app_source", "deliveryConfig": domain.Object{"registrySync": domain.Object{"enabled": true, "imageRepository": "registry.example.test/team/app", "registryUsername": "example", "registrySecretRef": registry["secretRef"]}}}
	if err := store.MutateState(ctx, func(state *domain.State) error { state.ApplicationDeploymentConfigs = []domain.Object{cfg}; return nil }); err != nil {
		t.Fatal(err)
	}

	options := domain.BackupOptions{
		RequestedScopes:      []domain.BackupScope{domain.BackupScopeProjects},
		IncludeSensitiveData: true,
		IncludeLogsAndAudit:  false,
	}
	preview, err := runtime.PreviewBackup(ctx, domain.BackupPreviewRequest{Options: options})
	if err != nil {
		t.Fatalf("preview backup: %v", err)
	}
	if len(preview.Blockers) != 0 {
		t.Fatal("sensitive backup unexpectedly blocked")
	}
	archive, _, manifest, err := runtime.CreateBackupArchive(ctx, domain.BackupDownloadRequest{
		Options:       options,
		PreviewDigest: preview.PreviewDigest,
		Passphrase:    "correct-horse-battery-staple",
	})
	if err != nil {
		t.Fatalf("create backup archive: %v", err)
	}
	archivePath := archive.Name()
	defer func() {
		_ = archive.Close()
		_ = os.Remove(archivePath)
	}()
	if !manifest.IncludesSensitiveData || manifest.RecordCounts["secretMaterials"] != 3 {
		t.Fatal("sensitive manifest is incomplete")
	}

	resetStoreToFreshState(t, store, ctx)
	if _, err := archive.Seek(0, 0); err != nil {
		t.Fatalf("rewind archive: %v", err)
	}
	upload, err := runtime.StageRestoreUpload(ctx, archive, "correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("stage restore: %v", err)
	}
	restoreOptions := domain.RestoreOptions{
		RequestedScopes:     []domain.BackupScope{domain.BackupScopeProjects},
		IncludeLogsAndAudit: false,
		ConflictPolicy:      domain.RestoreConflictAbortOnConflict,
	}
	restorePreview, err := runtime.PreviewRestore(ctx, domain.RestorePreviewRequest{UploadID: upload.UploadID, Options: restoreOptions})
	if err != nil {
		t.Fatalf("preview restore: %v", err)
	}
	if len(restorePreview.Blockers) != 0 || len(restorePreview.MissingSecretRefs) != 0 {
		t.Fatal("verified sensitive restore should be ready")
	}
	task, err := runtime.CreateRestoreTask(ctx, domain.RestoreExecuteRequest{
		UploadID:      upload.UploadID,
		Options:       restoreOptions,
		PreviewDigest: restorePreview.PreviewDigest,
	})
	if err != nil {
		t.Fatalf("create restore task: %v", err)
	}
	if task.Status != "pending_confirmation" {
		t.Fatal("restore must require confirmation")
	}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatalf("confirm restore task: %v", err)
	}
	if result, err := runtime.RunWorkerOnce(ctx); err != nil || result.Processed != 1 {
		t.Fatal("restore worker did not process exactly one task")
	}

	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load restored state: %v", err)
	}
	if findObject(state.Projects, "project_source") == nil || findObject(state.Applications, "app_source") == nil || findObject(state.Servers, "server_source") == nil {
		t.Fatal("business closure was not restored")
	}
	restoredConfig := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", "app_source")
	plan := domain.Object{"buildSnapshot": restoredConfig, "registryRevisionSecretRef": registry["materialRef"]}
	publish, err := runtime.registryPushMaterials(ctx, state, plan)
	if err != nil || publish["password"] != "example-publish" {
		t.Fatal("归档恢复未能保留仓库用途及材料修订", err)
	}
	asset := findObject(state.SecretAssets, "asset_source")
	if asset == nil || asset["materialStatus"] != "available" {
		t.Fatal("secret asset availability was not restored")
	}
	material, err := store.GetSecretMaterial(ctx, "secret_source")
	if err != nil || material != "synthetic-backup-value" {
		t.Fatal("restored credential differs from the test input")
	}
	if _, err := os.Stat(runtime.restoreUploadArchivePath(upload.UploadID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restore staging archive was not removed: %v", err)
	}
}

func TestReferenceOnlyRestoreMarksMissingMaterialWithoutBlockingMetadata(t *testing.T) {
	runtime, store, ctx := newBackupRestoreRuntime(t)
	seedBackupRestoreSource(t, store, ctx, true)
	service, err := runtime.backupArchiveService()
	if err != nil {
		t.Fatalf("create service: %v", err)
	}
	packageData, err := service.BuildPackage(ctx, domain.BackupOptions{
		RequestedScopes: []domain.BackupScope{domain.BackupScopeProjects},
	})
	if err != nil {
		t.Fatalf("build reference-only package: %v", err)
	}
	var archive bytes.Buffer
	if err := WriteBackupArchive(&archive, packageData, "", nil); err != nil {
		t.Fatalf("write reference-only archive: %v", err)
	}
	resetStoreToFreshState(t, store, ctx)
	upload, err := runtime.StageRestoreUpload(ctx, bytes.NewReader(archive.Bytes()), "")
	if err != nil {
		t.Fatalf("stage reference-only restore: %v", err)
	}
	options := domain.RestoreOptions{
		RequestedScopes: []domain.BackupScope{domain.BackupScopeProjects},
		ConflictPolicy:  domain.RestoreConflictAbortOnConflict,
	}
	preview, err := runtime.PreviewRestore(ctx, domain.RestorePreviewRequest{UploadID: upload.UploadID, Options: options})
	if err != nil {
		t.Fatalf("preview reference-only restore: %v", err)
	}
	if len(preview.Blockers) != 0 || len(preview.MissingSecretRefs) != 1 || preview.MissingSecretRefs[0] != "secret_source" {
		t.Fatal("unexpected missing-secret preview")
	}
	task, err := runtime.CreateRestoreTask(ctx, domain.RestoreExecuteRequest{UploadID: upload.UploadID, Options: options, PreviewDigest: preview.PreviewDigest})
	if err != nil {
		t.Fatalf("create reference-only restore task: %v", err)
	}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatalf("confirm restore: %v", err)
	}
	if _, err := runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatalf("run restore worker: %v", err)
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load restored state: %v", err)
	}
	asset := findObject(state.SecretAssets, "asset_source")
	if asset == nil || asset["materialStatus"] != "missing" {
		t.Fatal("reference-only secret was not marked missing")
	}
	if _, err := store.GetSecretMaterial(ctx, "secret_source"); err == nil {
		t.Fatal("reference-only restore synthesized secret material")
	}
}

func TestOverwriteRestorePreservesDestinationIDsAndUnselectedResources(t *testing.T) {
	sourceRuntime, sourceStore, sourceContext := newBackupRestoreRuntime(t)
	if err := sourceStore.MutateState(sourceContext, func(state *domain.State) error {
		state.Projects = append(state.Projects, domain.Object{"id": "project_source", "name": "Source", "slug": "same", "applicationIds": []string{"app_source"}, "serverIds": []string{}})
		state.Applications = append(state.Applications, domain.Object{"id": "app_source", "projectId": "project_source", "name": "API", "slug": "api", "sourceConfig": domain.Object{"provider": "git", "repositoryUrl": "https://example.test/source.git"}})
		state.Routes = append(state.Routes, domain.Object{"id": "route_source", "projectId": "project_source", "applicationId": "app_source", "domainIds": []string{}})
		return nil
	}); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	service, err := sourceRuntime.backupArchiveService()
	if err != nil {
		t.Fatalf("create source service: %v", err)
	}
	packageData, err := service.BuildPackage(sourceContext, domain.BackupOptions{RequestedScopes: []domain.BackupScope{domain.BackupScopeProjects}})
	if err != nil {
		t.Fatalf("build source package: %v", err)
	}
	var archive bytes.Buffer
	if err := WriteBackupArchive(&archive, packageData, "", nil); err != nil {
		t.Fatalf("write source archive: %v", err)
	}

	targetRuntime, targetStore, targetContext := newBackupRestoreRuntime(t)
	if err := targetStore.MutateState(targetContext, func(state *domain.State) error {
		state.Projects = append(state.Projects,
			domain.Object{"id": "project_target", "name": "Target", "slug": "same", "applicationIds": []string{"app_target"}, "serverIds": []string{}},
			domain.Object{"id": "project_unrelated", "name": "Unrelated", "slug": "unrelated", "applicationIds": []string{}, "serverIds": []string{}},
		)
		state.Applications = append(state.Applications, domain.Object{"id": "app_target", "projectId": "project_target", "name": "Old API", "slug": "api", "sourceConfig": domain.Object{"provider": "git", "repositoryUrl": "https://example.test/old.git"}})
		return nil
	}); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	upload, err := targetRuntime.StageRestoreUpload(targetContext, bytes.NewReader(archive.Bytes()), "")
	if err != nil {
		t.Fatalf("stage target restore: %v", err)
	}
	options := domain.RestoreOptions{
		RequestedScopes: []domain.BackupScope{domain.BackupScopeProjects},
		ConflictPolicy:  domain.RestoreConflictOverwriteSelected,
	}
	preview, err := targetRuntime.PreviewRestore(targetContext, domain.RestorePreviewRequest{UploadID: upload.UploadID, Options: options})
	if err != nil {
		t.Fatalf("preview overwrite restore: %v", err)
	}
	if len(preview.Conflicts) < 2 || len(preview.Blockers) != 0 {
		t.Fatal("overwrite preview did not expose resolvable conflicts")
	}
	task, err := targetRuntime.CreateRestoreTask(targetContext, domain.RestoreExecuteRequest{UploadID: upload.UploadID, Options: options, PreviewDigest: preview.PreviewDigest})
	if err != nil {
		t.Fatalf("create overwrite task: %v", err)
	}
	if task.Action != string(domain.TaskActionRestoreBackupOverwrite) || task.Status != "pending_confirmation" {
		t.Fatal("overwrite restore did not require dangerous confirmation")
	}
	if _, err := targetRuntime.ConfirmTask(targetContext, task.ID); err != nil {
		t.Fatalf("confirm overwrite: %v", err)
	}
	if _, err := targetRuntime.RunWorkerOnce(targetContext); err != nil {
		t.Fatalf("run overwrite worker: %v", err)
	}
	state, err := targetStore.LoadState(targetContext)
	if err != nil {
		t.Fatalf("load target state: %v", err)
	}
	if findObject(state.Projects, "project_target") == nil || findObject(state.Projects, "project_unrelated") == nil || findObject(state.Projects, "project_source") != nil {
		t.Fatal("project ID preservation or unselected resource preservation failed")
	}
	application := findObject(state.Applications, "app_target")
	if application == nil || application["projectId"] != "project_target" || application["name"] != "API" {
		t.Fatal("application conflict was not rewritten onto the local IDs")
	}
	if len(state.Routes) != 1 || state.Routes[0]["projectId"] != "project_target" || state.Routes[0]["applicationId"] != "app_target" {
		t.Fatal("restored references were not rewritten")
	}
}

func TestRewriteRestoreValueMapsTargetServersWithoutRewritingPlainStrings(t *testing.T) {
	value := domain.Object{
		"targetServers": []string{"server_source"},
		"displayName":   "server_source",
		"nested": domain.Object{
			"targetServers": []any{"server_source"},
		},
	}

	rewriteRestoreValue(value, map[string]string{"server_source": "server_target"}, nil)

	if got := domain.StringSliceFromAny(value["targetServers"]); len(got) != 1 || got[0] != "server_target" {
		t.Fatal("targetServers were not rewritten")
	}
	if value["displayName"] != "server_source" {
		t.Fatal("plain string was rewritten as an ID reference")
	}
	nested, ok := value["nested"].(domain.Object)
	if !ok {
		t.Fatal("nested object changed type")
	}
	if got := domain.StringSliceFromAny(nested["targetServers"]); len(got) != 1 || got[0] != "server_target" {
		t.Fatal("nested targetServers were not rewritten")
	}
}

func TestRestorePlanReportsIDConflictAndOnlyRemapsInOverwriteMode(t *testing.T) {
	current := domain.EmptyState()
	current.Projects = []domain.Object{{"id": "project_same", "slug": "existing", "name": "Existing"}}
	payload := domain.BackupPayload{
		SchemaVersion: domain.BackupSchemaVersion,
		Scopes:        []domain.BackupScope{domain.BackupScopeProjects},
		Projects: &domain.BackupProjectsData{
			Projects:                     []domain.Object{{"id": "project_same"}},
			Applications:                 []domain.Object{},
			ApplicationDeploymentConfigs: []domain.Object{},
			Routes:                       []domain.Object{},
			UploadFiles:                  []domain.BackupFileMetadata{},
		},
	}

	plan := buildRestoreObjectPlan(current, payload, payload.Scopes, "upload_conflict", "")
	if len(plan.conflicts) != 1 || len(plan.conflicts[0].ReasonCodes) != 1 || plan.conflicts[0].ReasonCodes[0] != "ID_CONFLICT" || plan.conflicts[0].NaturalKey != "id=project_same" {
		t.Fatal("ID-only collision was not reported")
	}
	if err := applyRestoreObjectPlan(&current, plan, domain.RestoreConflictAbortOnConflict); err == nil || err.Error() != "RESTORE_CONFLICTS_PRESENT" {
		t.Fatalf("abort policy did not block ID conflict: %v", err)
	}
	if len(current.Projects) != 1 || current.Projects[0]["slug"] != "existing" {
		t.Fatal("abort policy changed destination state")
	}

	if err := applyRestoreObjectPlan(&current, plan, domain.RestoreConflictOverwriteSelected); err != nil {
		t.Fatalf("overwrite policy did not safely remap ID conflict: %v", err)
	}
	if len(current.Projects) != 2 || findObject(current.Projects, "project_same") == nil {
		t.Fatal("overwrite did not preserve the existing project")
	}
	remappedID := plan.idMap["project_same"]
	if remappedID == "" || remappedID == "project_same" || findObject(current.Projects, remappedID) == nil {
		t.Fatal("incoming project was not remapped safely")
	}
}

func TestRestoreWorkerRechecksActivityAndDiscardsFailedUpload(t *testing.T) {
	runtime, store, ctx := newBackupRestoreRuntime(t)
	seedBackupRestoreSource(t, store, ctx, false)
	options := domain.BackupOptions{RequestedScopes: []domain.BackupScope{domain.BackupScopeProjects}}
	preview, err := runtime.PreviewBackup(ctx, domain.BackupPreviewRequest{Options: options})
	if err != nil {
		t.Fatalf("preview backup: %v", err)
	}
	archive, _, _, err := runtime.CreateBackupArchive(ctx, domain.BackupDownloadRequest{Options: options, PreviewDigest: preview.PreviewDigest})
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	archivePath := archive.Name()
	defer func() {
		_ = archive.Close()
		_ = os.Remove(archivePath)
	}()
	resetStoreToFreshState(t, store, ctx)
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("rewind archive: %v", err)
	}
	upload, err := runtime.StageRestoreUpload(ctx, archive, "")
	if err != nil {
		t.Fatalf("stage restore: %v", err)
	}
	restoreOptions := domain.RestoreOptions{RequestedScopes: []domain.BackupScope{domain.BackupScopeProjects}, ConflictPolicy: domain.RestoreConflictAbortOnConflict}
	restorePreview, err := runtime.PreviewRestore(ctx, domain.RestorePreviewRequest{UploadID: upload.UploadID, Options: restoreOptions})
	if err != nil {
		t.Fatalf("preview restore: %v", err)
	}
	task, err := runtime.CreateRestoreTask(ctx, domain.RestoreExecuteRequest{UploadID: upload.UploadID, Options: restoreOptions, PreviewDigest: restorePreview.PreviewDigest})
	if err != nil {
		t.Fatalf("create restore task: %v", err)
	}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatalf("confirm restore task: %v", err)
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Tasks = append(state.Tasks, domain.Task{
			ID: "task_concurrent", Action: "deploy_application", Status: "executing", TargetKind: "application", TargetID: "app_other",
			Payload: map[string]any{}, CreatedAt: domain.Now(), UpdatedAt: domain.Now(),
		})
		return nil
	}); err != nil {
		t.Fatalf("seed concurrent task: %v", err)
	}
	if result, err := runtime.RunWorkerOnce(ctx); err != nil || result.Processed != 1 {
		t.Fatal("restore worker did not process exactly one task")
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load failed restore state: %v", err)
	}
	restoredTask := findTask(state.Tasks, task.ID)
	if restoredTask == nil || restoredTask.Status != "failed" {
		t.Fatal("restore task did not fail on concurrent activity")
	}
	for _, job := range state.BackupRestoreJobs {
		if domain.StringValue(job["id"]) == upload.UploadID {
			t.Fatal("failed restore upload session remains")
		}
	}
	if _, err := os.Stat(runtime.restoreUploadArchivePath(upload.UploadID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed restore archive remains: %v", err)
	}
}

func TestCancellingSensitiveRestoreDiscardsSessionKeyAndArchive(t *testing.T) {
	runtime, store, ctx := newBackupRestoreRuntime(t)
	seedBackupRestoreSource(t, store, ctx, true)
	options := domain.BackupOptions{RequestedScopes: []domain.BackupScope{domain.BackupScopeProjects}, IncludeSensitiveData: true}
	preview, err := runtime.PreviewBackup(ctx, domain.BackupPreviewRequest{Options: options})
	if err != nil {
		t.Fatalf("preview sensitive backup: %v", err)
	}
	archive, _, _, err := runtime.CreateBackupArchive(ctx, domain.BackupDownloadRequest{
		Options: options, PreviewDigest: preview.PreviewDigest, Passphrase: "correct-horse-battery-staple",
	})
	if err != nil {
		t.Fatalf("create sensitive backup: %v", err)
	}
	archivePath := archive.Name()
	defer func() {
		_ = archive.Close()
		_ = os.Remove(archivePath)
	}()
	resetStoreToFreshState(t, store, ctx)
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("rewind archive: %v", err)
	}
	upload, err := runtime.StageRestoreUpload(ctx, archive, "correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("stage sensitive restore: %v", err)
	}
	session, err := runtime.restoreUploadSession(ctx, upload.UploadID)
	if err != nil || session.SessionSecretRef == "" {
		t.Fatal("sensitive restore session missing its expected fields")
	}
	if exists, err := store.HasSecretMaterial(ctx, session.SessionSecretRef); err != nil || !exists {
		t.Fatalf("session key was not persisted securely: exists=%v err=%v", exists, err)
	}
	restoreOptions := domain.RestoreOptions{RequestedScopes: []domain.BackupScope{domain.BackupScopeProjects}, ConflictPolicy: domain.RestoreConflictAbortOnConflict}
	restorePreview, err := runtime.PreviewRestore(ctx, domain.RestorePreviewRequest{UploadID: upload.UploadID, Options: restoreOptions})
	if err != nil {
		t.Fatalf("preview sensitive restore: %v", err)
	}
	task, err := runtime.CreateRestoreTask(ctx, domain.RestoreExecuteRequest{UploadID: upload.UploadID, Options: restoreOptions, PreviewDigest: restorePreview.PreviewDigest})
	if err != nil {
		t.Fatalf("create sensitive restore task: %v", err)
	}
	if _, err := runtime.CancelTask(ctx, task.ID); err != nil {
		t.Fatalf("cancel sensitive restore: %v", err)
	}
	if exists, err := store.HasSecretMaterial(ctx, session.SessionSecretRef); err != nil || exists {
		t.Fatalf("cancelled restore session key remains: exists=%v err=%v", exists, err)
	}
	if _, err := os.Stat(runtime.restoreUploadArchivePath(upload.UploadID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled restore archive remains: %v", err)
	}
	if _, err := runtime.restoreUploadSession(ctx, upload.UploadID); err == nil || err.Error() != "RESTORE_UPLOAD_NOT_FOUND" {
		t.Fatalf("cancelled restore session remains: %v", err)
	}
}

func TestBackupPreviewDigestIncludesSelectedOperationAndRuntimeLogs(t *testing.T) {
	runtime, store, ctx := newBackupRestoreRuntime(t)
	seedBackupRestoreSource(t, store, ctx, false)
	options := domain.BackupOptions{
		RequestedScopes:     []domain.BackupScope{domain.BackupScopeProjects},
		IncludeLogsAndAudit: true,
	}
	preview, err := runtime.PreviewBackup(ctx, domain.BackupPreviewRequest{Options: options})
	if err != nil {
		t.Fatalf("preview backup: %v", err)
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.OperationLogs = append(state.OperationLogs, domain.OperationLog{
			ID: "operation_after_preview", Action: "save_application", Status: "succeeded", TargetKind: "project", TargetID: "project_source",
			Level: "info", Message: "updated", Timestamp: domain.Now(),
		})
		return nil
	}); err != nil {
		t.Fatalf("append post-preview history: %v", err)
	}
	if _, _, _, err := runtime.CreateBackupArchive(ctx, domain.BackupDownloadRequest{Options: options, PreviewDigest: preview.PreviewDigest}); err == nil || err.Error() != "BACKUP_PREVIEW_STALE" {
		t.Fatalf("history change did not invalidate backup preview: %v", err)
	}
}
