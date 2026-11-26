package domain

const BackupSchemaVersion = "2"

type BackupPreviewRequest struct {
	Options BackupOptions `json:"options"`
}

type BackupPreviewResponse struct {
	Manifest        BackupManifest `json:"manifest"`
	PreviewDigest   string         `json:"previewDigest"`
	AvailableScopes []BackupScope  `json:"availableScopes"`
	Blockers        []string       `json:"blockers"`
	Warnings        []string       `json:"warnings"`
}

type BackupDownloadRequest struct {
	Options       BackupOptions `json:"options"`
	PreviewDigest string        `json:"previewDigest"`
	Passphrase    string        `json:"passphrase,omitempty"`
}

type RestoreUploadResponse struct {
	UploadID  string         `json:"uploadId"`
	ExpiresAt string         `json:"expiresAt"`
	Manifest  BackupManifest `json:"manifest"`
}

type RestorePreviewRequest struct {
	UploadID string         `json:"uploadId"`
	Options  RestoreOptions `json:"options"`
}

type RestoreConflict struct {
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	SourceID      string   `json:"sourceId"`
	DestinationID string   `json:"destinationId,omitempty"`
	NaturalKey    string   `json:"naturalKey"`
	ReasonCodes   []string `json:"reasonCodes"`
	Summary       string   `json:"summary"`
}

type RestorePreviewResponse struct {
	UploadID          string             `json:"uploadId"`
	PreviewDigest     string             `json:"previewDigest"`
	StateDigest       string             `json:"stateDigest"`
	Manifest          BackupManifest     `json:"manifest"`
	AvailableScopes   []BackupScope      `json:"availableScopes"`
	SelectedScopes    []BackupScope      `json:"selectedScopes"`
	DependencyReasons []BackupDependency `json:"dependencyReasons"`
	Conflicts         []RestoreConflict  `json:"conflicts"`
	MissingSecretRefs []string           `json:"missingSecretRefs"`
	Blockers          []string           `json:"blockers"`
	Warnings          []string           `json:"warnings"`
}

type RestoreExecuteRequest struct {
	UploadID      string         `json:"uploadId"`
	Options       RestoreOptions `json:"options"`
	PreviewDigest string         `json:"previewDigest"`
}
