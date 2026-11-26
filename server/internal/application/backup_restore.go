package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const restoreUploadTTL = 30 * time.Minute

type restoreUploadSession struct {
	ID               string                `json:"id"`
	Kind             string                `json:"kind"`
	Status           string                `json:"status"`
	ArchiveSHA256    string                `json:"archiveSha256"`
	SessionSecretRef string                `json:"sessionSecretRef,omitempty"`
	Manifest         domain.BackupManifest `json:"manifest"`
	TaskID           string                `json:"taskId,omitempty"`
	CreatedAt        string                `json:"createdAt"`
	ExpiresAt        string                `json:"expiresAt"`
}

type restoreTaskPayload struct {
	UploadID      string                `json:"uploadId"`
	Options       domain.RestoreOptions `json:"options"`
	PreviewDigest string                `json:"previewDigest"`
	StateDigest   string                `json:"stateDigest"`
	ArchiveSHA256 string                `json:"archiveSha256"`
}

type preparedRestoreFile struct {
	stagedPath      string
	destinationPath string
	rollbackPath    string
	installed       bool
	hadDestination  bool
}

type preparedRestoreFiles struct {
	root  string
	items []preparedRestoreFile
}

func (r *Runtime) PreviewBackup(ctx context.Context, request domain.BackupPreviewRequest) (domain.BackupPreviewResponse, error) {
	service, err := r.backupArchiveService()
	if err != nil {
		return domain.BackupPreviewResponse{}, err
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return domain.BackupPreviewResponse{}, err
	}
	requested, err := domain.NormalizeBackupScopes(request.Options.RequestedScopes)
	if err != nil {
		return domain.BackupPreviewResponse{}, err
	}
	effective, dependencies, err := domain.ResolveBackupScopeClosure(state, requested)
	if err != nil {
		return domain.BackupPreviewResponse{}, err
	}
	payload := domain.BuildBackupPayload(state, service.SchemaVersion, effective, request.Options.IncludeLogsAndAudit)
	blockers := []string{}
	warnings := []string{}
	secretRefs := domain.BackupPayloadSecretRefs(payload)
	if request.Options.IncludeSensitiveData {
		for _, ref := range secretRefs {
			exists, existsErr := r.Store.HasSecretMaterial(ctx, ref)
			if existsErr != nil {
				return domain.BackupPreviewResponse{}, existsErr
			}
			if !exists {
				blockers = append(blockers, "BACKUP_SECRET_MATERIAL_MISSING:"+ref)
			}
		}
	} else if len(secretRefs) > 0 {
		warnings = append(warnings, "BACKUP_SENSITIVE_DATA_EXCLUDED")
	}
	if !request.Options.IncludeLogsAndAudit {
		warnings = append(warnings, "BACKUP_LOGS_AND_AUDIT_EXCLUDED")
	}
	if payload.Projects != nil {
		blockers = append(blockers, backupUploadMetadataBlockers(payload.Projects)...)
		for _, metadata := range payload.Projects.UploadFiles {
			if fileErr := r.verifyBackupUploadFile(metadata); fileErr != nil {
				blockers = append(blockers, fileErr.Error())
			}
		}
	}
	counts := domain.BackupPayloadCounts(payload)
	if request.Options.IncludeSensitiveData {
		counts["secretMaterials"] = len(secretRefs)
	}
	manifest := domain.BackupManifest{
		ApplicationVersion:    service.ApplicationVersion,
		SchemaVersion:         service.SchemaVersion,
		RequestedScopes:       requested,
		EffectiveScopes:       effective,
		DependencyReasons:     dependencies,
		RecordCounts:          counts,
		IncludesSensitiveData: request.Options.IncludeSensitiveData,
		IncludesLogsAndAudit:  request.Options.IncludeLogsAndAudit,
		PayloadSHA256:         "",
		CreatedAt:             domain.Now(),
	}
	previewDigest, err := backupPreviewDigest(request.Options, payload, service)
	if err != nil {
		return domain.BackupPreviewResponse{}, err
	}
	sort.Strings(blockers)
	sort.Strings(warnings)
	return domain.BackupPreviewResponse{
		Manifest:        manifest,
		PreviewDigest:   previewDigest,
		AvailableScopes: domain.BackupScopes(),
		Blockers:        uniqueStrings(blockers),
		Warnings:        uniqueStrings(warnings),
	}, nil
}

func backupUploadMetadataBlockers(projects *domain.BackupProjectsData) []string {
	if projects == nil {
		return nil
	}
	blockers := []string{}
	for _, group := range [][]domain.Object{projects.Applications, projects.ApplicationDeploymentConfigs} {
		for _, item := range group {
			source := objectMapFromAny(item["sourceConfig"])
			if domain.StringValue(source["provider"]) != "upload" {
				continue
			}
			assetID := domain.StringValue(source["artifactAssetId"])
			sha := domain.StringValue(source["artifactSha256"])
			if assetID == "" || len(sha) != sha256.Size*2 || source["artifactSizeBytes"] == nil {
				blockers = append(blockers, "BACKUP_UPLOAD_METADATA_MISSING:"+domain.StringValue(item["id"]))
			}
		}
	}
	return blockers
}

func (r *Runtime) CreateBackupArchive(ctx context.Context, request domain.BackupDownloadRequest) (*os.File, string, domain.BackupManifest, error) {
	if request.Options.IncludeSensitiveData {
		if !validBackupPassword(request.Passphrase) {
			return nil, "", domain.BackupManifest{}, ErrBackupPasswordTooShort
		}
	} else if request.Passphrase != "" {
		return nil, "", domain.BackupManifest{}, errors.New("BACKUP_PASSPHRASE_NOT_ALLOWED")
	}
	preview, err := r.PreviewBackup(ctx, domain.BackupPreviewRequest{Options: request.Options})
	if err != nil {
		return nil, "", domain.BackupManifest{}, err
	}
	if preview.PreviewDigest != request.PreviewDigest {
		return nil, "", domain.BackupManifest{}, errors.New("BACKUP_PREVIEW_STALE")
	}
	if len(preview.Blockers) > 0 {
		return nil, "", domain.BackupManifest{}, errors.New(preview.Blockers[0])
	}
	service, err := r.backupArchiveService()
	if err != nil {
		return nil, "", domain.BackupManifest{}, err
	}
	packageData, err := service.BuildPackage(ctx, request.Options)
	if err != nil {
		return nil, "", domain.BackupManifest{}, err
	}
	packagePreviewDigest, err := backupPreviewDigest(request.Options, packageData.Payload, service)
	if err != nil {
		return nil, "", domain.BackupManifest{}, err
	}
	if packagePreviewDigest != request.PreviewDigest {
		return nil, "", domain.BackupManifest{}, errors.New("BACKUP_PREVIEW_STALE")
	}
	files, closers, err := r.openBackupSourceFiles(packageData)
	if err != nil {
		return nil, "", domain.BackupManifest{}, err
	}
	defer closeAll(closers)
	stagingDir := filepath.Join(r.Config.StateDir, "backup-staging")
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return nil, "", domain.BackupManifest{}, err
	}
	archive, err := os.CreateTemp(stagingDir, "iquops-backup-*.iquops-backup")
	if err != nil {
		return nil, "", domain.BackupManifest{}, err
	}
	removeArchive := true
	defer func() {
		if removeArchive {
			_ = archive.Close()
			_ = os.Remove(archive.Name())
		}
	}()
	if err := archive.Chmod(0o600); err != nil {
		return nil, "", domain.BackupManifest{}, err
	}
	if err := WriteBackupArchive(archive, packageData, request.Passphrase, files); err != nil {
		return nil, "", domain.BackupManifest{}, err
	}
	if err := archive.Sync(); err != nil {
		return nil, "", domain.BackupManifest{}, err
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return nil, "", domain.BackupManifest{}, err
	}
	if err := r.recordBackupCompleted(ctx, packageData.Manifest); err != nil {
		return nil, "", domain.BackupManifest{}, err
	}
	filename := "iquops-" + time.Now().UTC().Format("20060102T150405Z") + ".iquops-backup"
	removeArchive = false
	return archive, filename, packageData.Manifest, nil
}

func (r *Runtime) StageRestoreUpload(ctx context.Context, source io.Reader, passphrase string) (domain.RestoreUploadResponse, error) {
	if source == nil {
		return domain.RestoreUploadResponse{}, errors.New("RESTORE_FILE_REQUIRED")
	}
	if err := r.cleanupExpiredRestoreUploads(ctx); err != nil {
		return domain.RestoreUploadResponse{}, err
	}
	opened, err := OpenBackupArchive(source, passphrase)
	if err != nil {
		return domain.RestoreUploadResponse{}, err
	}
	defer func() { _ = opened.Close() }()
	service, err := r.backupArchiveService()
	if err != nil {
		return domain.RestoreUploadResponse{}, err
	}
	if err := validateRestoreManifestCompatibility(opened.Manifest, service); err != nil {
		return domain.RestoreUploadResponse{}, err
	}
	uploadID := domain.NewID("upload")
	expiresAt := time.Now().UTC().Add(restoreUploadTTL).Format(time.RFC3339Nano)
	stagingDir := filepath.Join(r.Config.StateDir, "restore-staging")
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return domain.RestoreUploadResponse{}, err
	}
	temporary, err := os.CreateTemp(stagingDir, "."+uploadID+"-*.tmp")
	if err != nil {
		return domain.RestoreUploadResponse{}, err
	}
	temporaryPath := temporary.Name()
	finalPath := r.restoreUploadArchivePath(uploadID)
	cleanupTemporary := true
	defer func() {
		_ = temporary.Close()
		if cleanupTemporary {
			_ = os.Remove(temporaryPath)
			_ = os.Remove(finalPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return domain.RestoreUploadResponse{}, err
	}
	sessionPassphrase := ""
	sessionSecretRef := ""
	materials := map[string]string{}
	if opened.Manifest.IncludesSensitiveData {
		sessionPassphrase, err = randomRestoreSessionPassphrase()
		if err != nil {
			return domain.RestoreUploadResponse{}, err
		}
		sessionSecretRef = "restore_session_" + uploadID
		materials[sessionSecretRef] = sessionPassphrase
	}
	files, closers, err := openedArchiveReaders(opened)
	if err != nil {
		return domain.RestoreUploadResponse{}, err
	}
	defer closeAll(closers)
	hash := sha256.New()
	if err := WriteBackupArchive(io.MultiWriter(temporary, hash), opened.BackupPackage, sessionPassphrase, files); err != nil {
		return domain.RestoreUploadResponse{}, err
	}
	if err := temporary.Sync(); err != nil {
		return domain.RestoreUploadResponse{}, err
	}
	if err := temporary.Close(); err != nil {
		return domain.RestoreUploadResponse{}, err
	}
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return domain.RestoreUploadResponse{}, err
	}
	session := restoreUploadSession{
		ID:               uploadID,
		Kind:             "restore_upload",
		Status:           "staged",
		ArchiveSHA256:    hex.EncodeToString(hash.Sum(nil)),
		SessionSecretRef: sessionSecretRef,
		Manifest:         opened.Manifest,
		CreatedAt:        domain.Now(),
		ExpiresAt:        expiresAt,
	}
	object, err := domain.EncodeObject(session)
	if err != nil {
		return domain.RestoreUploadResponse{}, err
	}
	if err := r.Store.MutateStateWithSecretMaterials(ctx, materials, func(state *domain.State) error {
		state.BackupRestoreJobs = append(state.BackupRestoreJobs, object)
		return nil
	}); err != nil {
		return domain.RestoreUploadResponse{}, err
	}
	cleanupTemporary = false
	return domain.RestoreUploadResponse{UploadID: uploadID, ExpiresAt: expiresAt, Manifest: opened.Manifest}, nil
}

func (r *Runtime) PreviewRestore(ctx context.Context, request domain.RestorePreviewRequest) (domain.RestorePreviewResponse, error) {
	if err := r.cleanupExpiredRestoreUploads(ctx); err != nil {
		return domain.RestorePreviewResponse{}, err
	}
	session, opened, err := r.openRestoreUpload(ctx, request.UploadID)
	if err != nil {
		return domain.RestorePreviewResponse{}, err
	}
	defer func() { _ = opened.Close() }()
	return r.previewRestorePackage(ctx, session, opened.BackupPackage, request.Options, "")
}

func (r *Runtime) CreateRestoreTask(ctx context.Context, request domain.RestoreExecuteRequest) (domain.Task, error) {
	preview, err := r.PreviewRestore(ctx, domain.RestorePreviewRequest{UploadID: request.UploadID, Options: request.Options})
	if err != nil {
		return domain.Task{}, err
	}
	if preview.PreviewDigest != request.PreviewDigest {
		return domain.Task{}, errors.New("RESTORE_PREVIEW_STALE")
	}
	if len(preview.Blockers) > 0 {
		return domain.Task{}, errors.New(preview.Blockers[0])
	}
	session, err := r.restoreUploadSession(ctx, request.UploadID)
	if err != nil {
		return domain.Task{}, err
	}
	payload := restoreTaskPayload{
		UploadID:      request.UploadID,
		Options:       normalizedRestoreOptions(request.Options),
		PreviewDigest: preview.PreviewDigest,
		StateDigest:   preview.StateDigest,
		ArchiveSHA256: session.ArchiveSHA256,
	}
	payloadObject, err := domain.EncodeObject(payload)
	if err != nil {
		return domain.Task{}, err
	}
	action := string(domain.TaskActionRestoreBackup)
	if request.Options.ConflictPolicy == domain.RestoreConflictOverwriteSelected {
		action = string(domain.TaskActionRestoreBackupOverwrite)
	}
	task, err := r.CreateTask(ctx, action, "backup_restore", request.UploadID, "恢复备份", map[string]any(payloadObject))
	if err != nil {
		return domain.Task{}, err
	}
	_ = r.Store.MutateState(ctx, func(state *domain.State) error {
		for _, item := range state.BackupRestoreJobs {
			if domain.StringValue(item["id"]) == request.UploadID && domain.StringValue(item["kind"]) == "restore_upload" {
				item["status"] = "pending_confirmation"
				item["taskId"] = task.ID
			}
		}
		return nil
	})
	return task, nil
}

func (r *Runtime) executeRestoreTask(ctx context.Context, task domain.Task) (summary string, resultErr error) {
	cleanupUploadID := strings.TrimSpace(task.TargetID)
	defer func() {
		if resultErr == nil || cleanupUploadID == "" {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if cleanupErr := r.discardRestoreUpload(cleanupCtx, cleanupUploadID); cleanupErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("RESTORE_STAGING_CLEANUP_FAILED: %w", cleanupErr))
		}
	}()
	payload, err := domain.DecodeObject[restoreTaskPayload](domain.Object(task.Payload))
	if err != nil {
		return "", errors.New("RESTORE_TASK_PAYLOAD_INVALID")
	}
	if payload.UploadID == "" || payload.PreviewDigest == "" || payload.StateDigest == "" || payload.ArchiveSHA256 == "" {
		return "", errors.New("RESTORE_TASK_PAYLOAD_INVALID")
	}
	cleanupUploadID = payload.UploadID
	if task.Action == string(domain.TaskActionRestoreBackupOverwrite) && payload.Options.ConflictPolicy != domain.RestoreConflictOverwriteSelected {
		return "", errors.New("RESTORE_TASK_POLICY_MISMATCH")
	}
	if task.Action == string(domain.TaskActionRestoreBackup) && payload.Options.ConflictPolicy != domain.RestoreConflictAbortOnConflict {
		return "", errors.New("RESTORE_TASK_POLICY_MISMATCH")
	}
	session, opened, err := r.openRestoreUpload(ctx, payload.UploadID)
	if err != nil {
		return "", err
	}
	defer func() { _ = opened.Close() }()
	if session.ArchiveSHA256 != payload.ArchiveSHA256 {
		return "", errors.New("RESTORE_UPLOAD_REPLACED")
	}
	// OpenBackupArchive has already materialized a verified private scratch ZIP.
	// The uploaded envelope is no longer needed and is removed before any state
	// mutation so success and failure paths cannot leave a plaintext archive.
	if err := os.Remove(r.restoreUploadArchivePath(payload.UploadID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	current, err := r.Store.LoadState(ctx)
	if err != nil {
		return "", err
	}
	if blockers := restoreActivityBlockers(current, task.ID); len(blockers) > 0 {
		return "", errors.New(blockers[0])
	}
	stateDigest, err := r.restoreStateDigest(ctx, current)
	if err != nil {
		return "", err
	}
	if stateDigest != payload.StateDigest {
		return "", errors.New("RESTORE_STATE_CHANGED")
	}
	selected, dependencies, err := resolveRestoreScopes(opened.BackupPackage, payload.Options.RequestedScopes)
	if err != nil {
		return "", err
	}
	_ = dependencies
	if payload.Options.IncludeLogsAndAudit && !opened.Manifest.IncludesLogsAndAudit {
		return "", errors.New("RESTORE_LOGS_NOT_AVAILABLE")
	}
	previewDigest, err := restorePreviewDigest(session, payload.Options, selected, stateDigest)
	if err != nil {
		return "", err
	}
	if previewDigest != payload.PreviewDigest {
		return "", errors.New("RESTORE_PREVIEW_STALE")
	}
	filteredPayload := filterBackupPayload(opened.Payload, selected, payload.Options.IncludeLogsAndAudit)
	plan := buildRestoreObjectPlan(current, filteredPayload, selected, payload.UploadID, r.Config.StateDir)
	if payload.Options.ConflictPolicy == domain.RestoreConflictAbortOnConflict && len(plan.conflicts) > 0 {
		return "", errors.New("RESTORE_CONFLICTS_PRESENT")
	}
	materials, err := restoreSecretMaterials(filteredPayload, plan.secretMap)
	if err != nil {
		return "", err
	}
	missingRefs, err := r.missingRestoreSecretRefs(ctx, filteredPayload, plan.secretMap)
	if err != nil {
		return "", err
	}
	preparedFiles, err := r.prepareRestoreFiles(opened, filteredPayload, plan.idMap, payload.UploadID)
	if err != nil {
		return "", err
	}
	defer preparedFiles.cleanup()
	if err := preparedFiles.install(); err != nil {
		return "", err
	}
	rollbackFiles := true
	defer func() {
		if rollbackFiles {
			preparedFiles.rollback()
		}
	}()
	businessDigest, err := backupBusinessStateDigest(current)
	if err != nil {
		return "", err
	}
	deleteRefs := []string{}
	if session.SessionSecretRef != "" {
		deleteRefs = append(deleteRefs, session.SessionSecretRef)
	}
	err = r.Store.MutateStateWithSecretChangesAndRestoreOperationalHistory(ctx, materials, deleteRefs, nil, nil, func(state *domain.State) error {
		if blockers := restoreActivityBlockers(*state, task.ID); len(blockers) > 0 {
			return errors.New(blockers[0])
		}
		digest, err := backupBusinessStateDigest(*state)
		if err != nil {
			return err
		}
		if digest != businessDigest {
			return errors.New("RESTORE_STATE_CHANGED")
		}
		if err := applyRestoreObjectPlan(state, plan, payload.Options.ConflictPolicy); err != nil {
			return err
		}
		if payload.Options.IncludeLogsAndAudit {
			appendRestoreHistory(state, filteredPayload.LogsAndAudit, plan.idMap, plan.secretMap, plan.selectedID)
		}
		markRestoredSecretStatus(state, plan.selected, plan.selectedID, missingRefs)
		state.BackupRestoreJobs = removeRestoreUploadSession(state.BackupRestoreJobs, payload.UploadID)
		state.BackupRestoreJobs = append(state.BackupRestoreJobs, domain.Object{
			"id":                  domain.NewID("restore"),
			"kind":                "restore",
			"status":              "succeeded",
			"taskId":              task.ID,
			"sourceUploadId":      payload.UploadID,
			"requestedScopes":     payload.Options.RequestedScopes,
			"effectiveScopes":     selected,
			"includeLogsAndAudit": payload.Options.IncludeLogsAndAudit,
			"conflictPolicy":      payload.Options.ConflictPolicy,
			"missingSecretRefs":   missingRefs,
			"completedAt":         domain.Now(),
		})
		return nil
	})
	if err != nil {
		return "", err
	}
	rollbackFiles = false
	preparedFiles.commit()
	return fmt.Sprintf("restored %d scope(s) from verified backup", len(selected)), nil
}
