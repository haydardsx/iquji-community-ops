package domain

// OperationRun 是控制面保留的最小操作执行记录。
// 它描述一次确认后的动作与结果；队列状态和真实阶段日志通过独立查询组合，不返回任务私有输入。
type OperationRun struct {
	ID         string `json:"id"`
	Action     string `json:"action"`
	Status     string `json:"status"`
	TargetKind string `json:"targetKind"`
	TargetID   string `json:"targetId"`
	TargetName string `json:"targetName,omitempty"`
	Summary    string `json:"summary,omitempty"`
	ErrorCode  string `json:"errorCode,omitempty"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

type OperationLog struct {
	Event          string `json:"event"`
	ErrorCode      string `json:"errorCode,omitempty"`
	Source         string `json:"source"`
	ID             string `json:"id"`
	OperationRunID string `json:"operationRunId,omitempty"`
	Action         string `json:"action"`
	Status         string `json:"status"`
	TargetKind     string `json:"targetKind"`
	TargetID       string `json:"targetId"`
	Level          string `json:"level"`
	Message        string `json:"message"`
	Timestamp      string `json:"timestamp"`
}

type RuntimeLog struct {
	Event          string `json:"event"`
	ErrorCode      string `json:"errorCode,omitempty"`
	ID             string `json:"id"`
	OperationRunID string `json:"operationRunId,omitempty"`
	Source         string `json:"source"`
	Phase          string `json:"phase"`
	Level          string `json:"level"`
	Message        string `json:"message"`
	Timestamp      string `json:"timestamp"`
	TargetKind     string `json:"targetKind"`
	TargetID       string `json:"targetId"`
	ErrorSummary   string `json:"errorSummary,omitempty"`
}

func IsOperationRunStatus(status string) bool {
	switch status {
	case "queued", "running", "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func IsOperationRunStatusTerminal(status string) bool {
	switch status {
	case "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func NewOperationRun(action, targetKind, targetID, targetName string) OperationRun {
	now := Now()
	return OperationRun{
		ID:         NewID("operation"),
		Action:     action,
		Status:     "queued",
		TargetKind: targetKind,
		TargetID:   targetID,
		TargetName: targetName,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func OperationRunResponse(run OperationRun) map[string]any {
	result := map[string]any{
		"id":         run.ID,
		"action":     run.Action,
		"status":     run.Status,
		"targetKind": run.TargetKind,
		"targetId":   run.TargetID,
		"targetName": run.TargetName,
		"summary":    run.Summary,
		"createdAt":  run.CreatedAt,
		"updatedAt":  run.UpdatedAt,
	}
	if run.ErrorCode != "" {
		result["errorCode"] = run.ErrorCode
	}
	return result
}
