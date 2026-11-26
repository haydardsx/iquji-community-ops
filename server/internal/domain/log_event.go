package domain

import (
	"regexp"
	"strings"
	"time"
)

var logIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func IsLogIdentifier(value string) bool {
	return len(value) <= 120 && logIdentifier.MatchString(value)
}

func IsLogLevel(value string) bool {
	switch value {
	case "debug", "info", "warn", "error":
		return true
	}
	return false
}

func LogStatusLevel(status string) string {
	switch status {
	case "failed", "rolled_back":
		return "error"
	case "cancelled":
		return "warn"
	default:
		return "info"
	}
}

func NewRuntimeLog(task Task, source, phase, level, message, timestamp string) RuntimeLog {
	entry := RuntimeLog{
		ID: NewID("runtime_log"), OperationRunID: task.ID,
		Source: source, Phase: phase, Level: level, Message: message, Timestamp: timestamp,
		TargetKind: task.TargetKind, TargetID: task.TargetID,
	}
	entry.Normalize()
	return entry
}

func NewOperationLog(task Task, source, level, message, timestamp string) OperationLog {
	entry := OperationLog{
		ID: NewID("operation_log"), OperationRunID: task.ID, Action: task.Action, Status: task.Status,
		TargetKind: task.TargetKind, TargetID: task.TargetID,
		Source: source, Level: level, Message: message, Timestamp: timestamp,
	}
	entry.Normalize()
	return entry
}

func normalizedLogFields(timestamp, level, message string) (string, string, string) {
	if parsed, err := time.Parse(time.RFC3339Nano, timestamp); err == nil {
		timestamp = parsed.UTC().Format(time.RFC3339Nano)
	}
	if level == "warning" {
		level = "warn"
	}
	if !IsLogLevel(level) {
		level = "info"
	}
	return timestamp, level, LogSummary(message)
}

func messageErrorCode(level, message string) string {
	if level != "error" {
		return ""
	}
	end := strings.IndexAny(message, " ；：:")
	if end < 0 {
		end = len(message)
	}
	code := message[:end]
	if IsPublicDiagnosticCode(code) {
		return code
	}
	return "OPERATION_FAILED"
}

// Normalize 让历史数据与新写入的事件使用相同字段；不生成历史时间或操作归属。
func (entry *RuntimeLog) Normalize() {
	entry.Timestamp, entry.Level, entry.Message = normalizedLogFields(entry.Timestamp, entry.Level, entry.Message)
	if !IsLogIdentifier(entry.Source) {
		entry.Source = "worker"
	}
	if !IsLogIdentifier(entry.Phase) {
		entry.Phase = "operation"
	}
	entry.Event = entry.Source + "." + entry.Phase
	entry.ErrorCode = messageErrorCode(entry.Level, entry.Message)
	entry.ErrorSummary = LogSummary(entry.ErrorSummary)
}

func (entry *OperationLog) Normalize() {
	entry.Timestamp, entry.Level, entry.Message = normalizedLogFields(entry.Timestamp, entry.Level, entry.Message)
	if entry.Source == "" {
		entry.Source = "control"
	}
	entry.Event = "operation." + entry.Action + "." + entry.Status
	entry.ErrorCode = messageErrorCode(entry.Level, entry.Message)
}

func (state *State) NormalizeLogHistory() {
	outcomes := make(map[string]string, len(state.OperationRuns))
	for _, run := range state.OperationRuns {
		outcomes[run.ID] = run.Status
	}
	for i := range state.RuntimeLogs {
		entry := &state.RuntimeLogs[i]
		if entry.Event != "" {
			continue
		}
		if entry.Phase == "complete" && IsOperationRunStatusTerminal(outcomes[entry.OperationRunID]) {
			entry.Level = LogStatusLevel(outcomes[entry.OperationRunID])
		}
		entry.Normalize()
	}
	for i := range state.OperationLogs {
		entry := &state.OperationLogs[i]
		if entry.Event != "" {
			continue
		}
		if entry.Status == "failed" || entry.Status == "rolled_back" {
			entry.Level = "error"
		}
		entry.Normalize()
	}
}
