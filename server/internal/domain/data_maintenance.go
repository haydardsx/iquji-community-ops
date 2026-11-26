package domain

type DataMaintenanceAction string

const (
	DataMaintenanceClearLogs    DataMaintenanceAction = "clear_logs"
	DataMaintenanceFactoryReset DataMaintenanceAction = "factory_reset"
)

type DataMaintenancePreview struct {
	Action             DataMaintenanceAction `json:"action"`
	ConfirmationToken  string                `json:"confirmationToken"`
	ConfirmationPhrase string                `json:"confirmationPhrase"`
	StateDigest        string                `json:"stateDigest"`
	ExpiresAt          string                `json:"expiresAt"`
	DeleteCounts       map[string]int        `json:"deleteCounts"`
	PreserveCounts     map[string]int        `json:"preserveCounts"`
	Blockers           []string              `json:"blockers"`
	Warnings           []string              `json:"warnings"`
}

type DataMaintenanceExecuteRequest struct {
	ConfirmationToken  string `json:"confirmationToken"`
	ConfirmationPhrase string `json:"confirmationPhrase"`
	StateDigest        string `json:"stateDigest"`
}

type DataMaintenanceResult struct {
	Action        DataMaintenanceAction `json:"action"`
	Status        string                `json:"status"`
	ClearedCounts map[string]int        `json:"clearedCounts"`
	CompletedAt   string                `json:"completedAt"`
}
