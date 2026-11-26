package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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

func (r *Runtime) previewRestorePackage(ctx context.Context, session restoreUploadSession, packageData domain.BackupPackage, options domain.RestoreOptions, ignoreTaskID string) (domain.RestorePreviewResponse, error) {
	if options.ConflictPolicy != domain.RestoreConflictAbortOnConflict && options.ConflictPolicy != domain.RestoreConflictOverwriteSelected {
		return domain.RestorePreviewResponse{}, errors.New("RESTORE_CONFLICT_POLICY_INVALID")
	}
	normalizedOptions := normalizedRestoreOptions(options)
	selected, dependencies, err := resolveRestoreScopes(packageData, normalizedOptions.RequestedScopes)
	if err != nil {
		return domain.RestorePreviewResponse{}, err
	}
	current, err := r.Store.LoadState(ctx)
	if err != nil {
		return domain.RestorePreviewResponse{}, err
	}
	filteredPayload := filterBackupPayload(packageData.Payload, selected, normalizedOptions.IncludeLogsAndAudit)
	plan := buildRestoreObjectPlan(current, filteredPayload, selected, session.ID, r.Config.StateDir)
	missingRefs, err := r.missingRestoreSecretRefs(ctx, filteredPayload, plan.secretMap)
	if err != nil {
		return domain.RestorePreviewResponse{}, err
	}
	blockers := restoreActivityBlockers(current, ignoreTaskID)
	if plan.outputError != nil {
		blockers = append(blockers, plan.outputError.Error())
	}
	warnings := []string{}
	if normalizedOptions.IncludeLogsAndAudit && !packageData.Manifest.IncludesLogsAndAudit {
		blockers = append(blockers, "RESTORE_LOGS_NOT_AVAILABLE")
	}
	if normalizedOptions.ConflictPolicy == domain.RestoreConflictAbortOnConflict && len(plan.conflicts) > 0 {
		blockers = append(blockers, "RESTORE_CONFLICTS_PRESENT")
	}
	if len(missingRefs) > 0 {
		warnings = append(warnings, "RESTORE_SECRET_MATERIAL_MISSING")
	}
	stateDigest, err := r.restoreStateDigest(ctx, current)
	if err != nil {
		return domain.RestorePreviewResponse{}, err
	}
	previewDigest, err := restorePreviewDigest(session, normalizedOptions, selected, stateDigest)
	if err != nil {
		return domain.RestorePreviewResponse{}, err
	}
	sort.Strings(blockers)
	sort.Strings(warnings)
	return domain.RestorePreviewResponse{
		UploadID:          session.ID,
		PreviewDigest:     previewDigest,
		StateDigest:       stateDigest,
		Manifest:          packageData.Manifest,
		AvailableScopes:   append([]domain.BackupScope(nil), packageData.Manifest.EffectiveScopes...),
		SelectedScopes:    selected,
		DependencyReasons: dependencies,
		Conflicts:         plan.conflicts,
		MissingSecretRefs: missingRefs,
		Blockers:          uniqueStrings(blockers),
		Warnings:          uniqueStrings(warnings),
	}, nil
}

func resolveRestoreScopes(packageData domain.BackupPackage, requested []domain.BackupScope) ([]domain.BackupScope, []domain.BackupDependency, error) {
	normalized, err := domain.NormalizeBackupScopes(requested)
	if err != nil {
		return nil, nil, err
	}
	available := backupScopeSet(packageData.Manifest.EffectiveScopes)
	for _, scope := range normalized {
		if !available[scope] {
			return nil, nil, errors.New("RESTORE_SCOPE_NOT_AVAILABLE:" + string(scope))
		}
	}
	packageState := backupPayloadState(packageData.Payload, packageData.Manifest.EffectiveScopes, false)
	selected, dependencies, err := domain.ResolveBackupScopeClosure(packageState, normalized)
	if err != nil {
		return nil, nil, err
	}
	for _, scope := range selected {
		if !available[scope] {
			return nil, nil, errors.New("RESTORE_DEPENDENCY_SCOPE_NOT_AVAILABLE:" + string(scope))
		}
	}
	return selected, dependencies, nil
}

func normalizedRestoreOptions(options domain.RestoreOptions) domain.RestoreOptions {
	requested, err := domain.NormalizeBackupScopes(options.RequestedScopes)
	if err == nil {
		options.RequestedScopes = requested
	}
	return options
}

func (r *Runtime) backupArchiveService() (*BackupArchiveService, error) {
	return NewBackupArchiveService(r.Store, r.Version, domain.BackupSchemaVersion), nil
}

func validateRestoreManifestCompatibility(manifest domain.BackupManifest, service *BackupArchiveService) error {
	if manifest.SchemaVersion != service.SchemaVersion {
		return errors.New("BACKUP_SCHEMA_INCOMPATIBLE")
	}
	if manifest.ApplicationVersion != service.ApplicationVersion {
		return errors.New("BACKUP_APPLICATION_VERSION_INCOMPATIBLE")
	}
	return nil
}

func backupBusinessStateDigest(state domain.State) (string, error) {
	payload := domain.BuildBackupPayload(state, domain.BackupSchemaVersion, domain.BackupScopes(), false)
	data, err := CanonicalBackupPayload(payload)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func (r *Runtime) restoreStateDigest(ctx context.Context, state domain.State) (string, error) {
	businessDigest, err := backupBusinessStateDigest(state)
	if err != nil {
		return "", err
	}
	presentRefs := []string{}
	refs := currentSecretRefs(state)
	orderedRefs := make([]string, 0, len(refs))
	for ref := range refs {
		orderedRefs = append(orderedRefs, ref)
	}
	sort.Strings(orderedRefs)
	for _, ref := range orderedRefs {
		exists, err := r.Store.HasSecretMaterial(ctx, ref)
		if err != nil {
			return "", err
		}
		if exists {
			presentRefs = append(presentRefs, ref)
		}
	}
	return digestJSON(struct {
		BusinessDigest string   `json:"businessDigest"`
		PresentRefs    []string `json:"presentSecretRefs"`
	}{BusinessDigest: businessDigest, PresentRefs: presentRefs})
}

func backupPreviewDigest(options domain.BackupOptions, payload domain.BackupPayload, service *BackupArchiveService) (string, error) {
	requested, err := domain.NormalizeBackupScopes(options.RequestedScopes)
	if err != nil {
		return "", err
	}
	logicalPayload := payload
	logicalPayload.SecretMaterials = nil
	payloadBytes, err := CanonicalBackupPayload(logicalPayload)
	if err != nil {
		return "", err
	}
	payloadHash := sha256.Sum256(payloadBytes)
	value := struct {
		Options            domain.BackupOptions `json:"options"`
		PayloadDigest      string               `json:"payloadDigest"`
		ApplicationVersion string               `json:"applicationVersion"`
		SchemaVersion      string               `json:"schemaVersion"`
	}{
		Options: domain.BackupOptions{
			RequestedScopes:      requested,
			IncludeSensitiveData: options.IncludeSensitiveData,
			IncludeLogsAndAudit:  options.IncludeLogsAndAudit,
		},
		PayloadDigest:      hex.EncodeToString(payloadHash[:]),
		ApplicationVersion: service.ApplicationVersion,
		SchemaVersion:      service.SchemaVersion,
	}
	return digestJSON(value)
}

func restorePreviewDigest(session restoreUploadSession, options domain.RestoreOptions, selected []domain.BackupScope, stateDigest string) (string, error) {
	value := struct {
		UploadID      string                `json:"uploadId"`
		ArchiveSHA256 string                `json:"archiveSha256"`
		PayloadSHA256 string                `json:"payloadSha256"`
		Options       domain.RestoreOptions `json:"options"`
		Selected      []domain.BackupScope  `json:"selectedScopes"`
		StateDigest   string                `json:"stateDigest"`
	}{
		UploadID:      session.ID,
		ArchiveSHA256: session.ArchiveSHA256,
		PayloadSHA256: session.Manifest.PayloadSHA256,
		Options:       normalizedRestoreOptions(options),
		Selected:      selected,
		StateDigest:   stateDigest,
	}
	return digestJSON(value)
}

func digestJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func (r *Runtime) verifyBackupUploadFile(metadata domain.BackupFileMetadata) error {
	path, err := r.backupUploadSourcePath(metadata)
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("BACKUP_UPLOAD_FILE_MISSING:%s", metadata.AssetID)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != metadata.SizeBytes {
		return fmt.Errorf("BACKUP_UPLOAD_FILE_SIZE_MISMATCH:%s", metadata.AssetID)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(metadata.SHA256) {
		return fmt.Errorf("BACKUP_UPLOAD_FILE_HASH_MISMATCH:%s", metadata.AssetID)
	}
	return nil
}

func validBackupAssetID(id string) bool {
	return filepath.IsLocal(id) && id != "." && !strings.ContainsAny(id, "/\\:\x00")
}

func (r *Runtime) backupUploadSourcePath(metadata domain.BackupFileMetadata) (string, error) {
	assetID := strings.TrimSpace(metadata.AssetID)
	if !validBackupAssetID(assetID) {
		return "", errors.New("BACKUP_UPLOAD_ASSET_ID_INVALID")
	}
	return filepath.Join(r.Config.StateDir, "uploads", assetID), nil
}

func (r *Runtime) openBackupSourceFiles(packageData domain.BackupPackage) ([]BackupArchiveFile, []io.Closer, error) {
	if packageData.Payload.Projects == nil {
		return []BackupArchiveFile{}, []io.Closer{}, nil
	}
	files := make([]BackupArchiveFile, 0, len(packageData.Payload.Projects.UploadFiles))
	closers := make([]io.Closer, 0, len(packageData.Payload.Projects.UploadFiles))
	for _, metadata := range packageData.Payload.Projects.UploadFiles {
		path, err := r.backupUploadSourcePath(metadata)
		if err != nil {
			closeAll(closers)
			return nil, nil, err
		}
		file, err := os.Open(path)
		if err != nil {
			closeAll(closers)
			return nil, nil, fmt.Errorf("%w:%s", ErrBackupFileMissing, metadata.AssetID)
		}
		files = append(files, BackupArchiveFile{ArchivePath: metadata.ArchivePath, Reader: file})
		closers = append(closers, file)
	}
	return files, closers, nil
}

func openedArchiveReaders(opened *OpenedBackupArchive) ([]BackupArchiveFile, []io.Closer, error) {
	files := []BackupArchiveFile{}
	closers := []io.Closer{}
	for _, name := range opened.FileNames() {
		reader, err := opened.OpenFile(name)
		if err != nil {
			closeAll(closers)
			return nil, nil, err
		}
		files = append(files, BackupArchiveFile{ArchivePath: name, Reader: reader})
		closers = append(closers, reader)
	}
	return files, closers, nil
}

func closeAll(closers []io.Closer) {
	for index := len(closers) - 1; index >= 0; index-- {
		_ = closers[index].Close()
	}
}

func (r *Runtime) recordBackupCompleted(ctx context.Context, manifest domain.BackupManifest) error {
	action := "create_backup"
	if manifest.IncludesSensitiveData {
		action = "create_sensitive_backup"
	}
	return r.Store.MutateState(ctx, func(state *domain.State) error {
		id := domain.NewID("backup")
		state.BackupRestoreJobs = append(state.BackupRestoreJobs, domain.Object{
			"id":                    id,
			"kind":                  "backup",
			"status":                "succeeded",
			"requestedScopes":       manifest.RequestedScopes,
			"effectiveScopes":       manifest.EffectiveScopes,
			"includesSensitiveData": manifest.IncludesSensitiveData,
			"includesLogsAndAudit":  manifest.IncludesLogsAndAudit,
			"recordCounts":          manifest.RecordCounts,
			"payloadSha256":         manifest.PayloadSHA256,
			"completedAt":           domain.Now(),
		})
		state.OperationLogs = append(state.OperationLogs, newOperationLog(action, "backup_restore", id, "succeeded", "已创建备份归档", ""))
		return nil
	})
}

func randomRestoreSessionPassphrase() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func (r *Runtime) restoreUploadArchivePath(uploadID string) string {
	return filepath.Join(r.Config.StateDir, "restore-staging", uploadID+".iquops-backup")
}

func (r *Runtime) restoreUploadSession(ctx context.Context, uploadID string) (restoreUploadSession, error) {
	if strings.TrimSpace(uploadID) == "" {
		return restoreUploadSession{}, errors.New("RESTORE_UPLOAD_ID_REQUIRED")
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return restoreUploadSession{}, err
	}
	for _, item := range state.BackupRestoreJobs {
		if domain.StringValue(item["id"]) != uploadID || domain.StringValue(item["kind"]) != "restore_upload" {
			continue
		}
		session, decodeErr := domain.DecodeObject[restoreUploadSession](item)
		if decodeErr != nil {
			return restoreUploadSession{}, errors.New("RESTORE_UPLOAD_SESSION_INVALID")
		}
		expiresAt, parseErr := time.Parse(time.RFC3339Nano, session.ExpiresAt)
		if parseErr != nil || !expiresAt.After(time.Now().UTC()) {
			return restoreUploadSession{}, errors.New("RESTORE_UPLOAD_EXPIRED")
		}
		return session, nil
	}
	return restoreUploadSession{}, errors.New("RESTORE_UPLOAD_NOT_FOUND")
}

func (r *Runtime) openRestoreUpload(ctx context.Context, uploadID string) (restoreUploadSession, *OpenedBackupArchive, error) {
	session, err := r.restoreUploadSession(ctx, uploadID)
	if err != nil {
		return restoreUploadSession{}, nil, err
	}
	path := r.restoreUploadArchivePath(uploadID)
	file, err := os.Open(path)
	if err != nil {
		return restoreUploadSession{}, nil, errors.New("RESTORE_UPLOAD_FILE_NOT_FOUND")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		_ = file.Close()
		return restoreUploadSession{}, nil, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != session.ArchiveSHA256 {
		_ = file.Close()
		return restoreUploadSession{}, nil, errors.New("RESTORE_UPLOAD_REPLACED")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return restoreUploadSession{}, nil, err
	}
	password := ""
	if session.SessionSecretRef != "" {
		password, err = r.Store.GetSecretMaterial(ctx, session.SessionSecretRef)
		if err != nil {
			_ = file.Close()
			return restoreUploadSession{}, nil, errors.New("RESTORE_SESSION_KEY_NOT_FOUND")
		}
	}
	opened, err := OpenBackupArchive(file, password)
	_ = file.Close()
	if err != nil {
		return restoreUploadSession{}, nil, err
	}
	if opened.Manifest.PayloadSHA256 != session.Manifest.PayloadSHA256 || opened.Manifest.SchemaVersion != session.Manifest.SchemaVersion {
		_ = opened.Close()
		return restoreUploadSession{}, nil, errors.New("RESTORE_UPLOAD_REPLACED")
	}
	return session, opened, nil
}

func (r *Runtime) cleanupExpiredRestoreUploads(ctx context.Context) error {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	expiredIDs := []string{}
	deleteRefs := []string{}
	for _, item := range state.BackupRestoreJobs {
		if domain.StringValue(item["kind"]) != "restore_upload" {
			continue
		}
		expiresAt, parseErr := time.Parse(time.RFC3339Nano, domain.StringValue(item["expiresAt"]))
		if parseErr == nil && expiresAt.After(now) {
			continue
		}
		expiredIDs = append(expiredIDs, domain.StringValue(item["id"]))
		if ref := domain.StringValue(item["sessionSecretRef"]); ref != "" {
			deleteRefs = append(deleteRefs, ref)
		}
	}
	if len(expiredIDs) == 0 {
		return nil
	}
	for _, id := range expiredIDs {
		if err := os.Remove(r.restoreUploadArchivePath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	expired := map[string]bool{}
	for _, id := range expiredIDs {
		expired[id] = true
	}
	if err := r.Store.MutateStateWithSecretChanges(ctx, nil, deleteRefs, func(current *domain.State) error {
		items := make([]domain.Object, 0, len(current.BackupRestoreJobs))
		for _, item := range current.BackupRestoreJobs {
			if domain.StringValue(item["kind"]) == "restore_upload" && expired[domain.StringValue(item["id"])] {
				continue
			}
			items = append(items, item)
		}
		current.BackupRestoreJobs = items
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (r *Runtime) discardRestoreUpload(ctx context.Context, uploadID string) error {
	uploadID = strings.TrimSpace(uploadID)
	if uploadID == "" {
		return nil
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return err
	}
	deleteRefs := []string{}
	for _, item := range state.BackupRestoreJobs {
		if domain.StringValue(item["kind"]) != "restore_upload" || domain.StringValue(item["id"]) != uploadID {
			continue
		}
		if ref := strings.TrimSpace(domain.StringValue(item["sessionSecretRef"])); ref != "" {
			deleteRefs = append(deleteRefs, ref)
		}
	}
	if err := os.Remove(r.restoreUploadArchivePath(uploadID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := r.Store.MutateStateWithSecretChanges(ctx, nil, deleteRefs, func(current *domain.State) error {
		current.BackupRestoreJobs = removeRestoreUploadSession(current.BackupRestoreJobs, uploadID)
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func removeRestoreUploadSession(items []domain.Object, uploadID string) []domain.Object {
	result := make([]domain.Object, 0, len(items))
	for _, item := range items {
		if domain.StringValue(item["kind"]) == "restore_upload" && domain.StringValue(item["id"]) == uploadID {
			continue
		}
		result = append(result, item)
	}
	return result
}

func restoreActivityBlockers(state domain.State, ignoreTaskID string) []string {
	blockers := []string{}
	for _, task := range state.Tasks {
		if task.ID != ignoreTaskID && domain.IsTaskActive(task.Status) {
			blockers = append(blockers, "RESTORE_ACTIVE_TASK")
			break
		}
	}
	for _, run := range state.JenkinsReleaseRuns {
		switch domain.StringValue(run["status"]) {
		case "pending", "triggered", "callback_received", "cleanup_required":
			blockers = append(blockers, "RESTORE_ACTIVE_JENKINS_RUN")
		}
	}
	for _, run := range state.FrpTunnelRuns {
		if run.Status.IsActive() {
			blockers = append(blockers, "RESTORE_ACTIVE_FRP_TUNNEL")
			break
		}
	}
	sort.Strings(blockers)
	return uniqueStrings(blockers)
}
