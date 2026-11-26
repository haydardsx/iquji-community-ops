package domain

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLogEventNormalizesLegacyLevelsAndProtectsMultilineSecrets(t *testing.T) {
	when := "2025-03-04T12:30:00+08:00"
	state := State{OperationRuns: []OperationRun{{ID: "run", Status: "failed"}}, RuntimeLogs: []RuntimeLog{
		{ID: "line", OperationRunID: "run", Source: "worker", Phase: "complete", Level: "info", Timestamp: when, Message: "TASK_EXECUTION_FAILED；token=sample"},
		{ID: "other", Source: "swarm", Phase: "check", Level: "warning", Timestamp: when, Message: "第一行\n第二行"},
	}}
	state.NormalizeLogHistory()
	entry := state.RuntimeLogs[0]
	if entry.Event != "worker.complete" || entry.ID != "line" || entry.Level != "error" || entry.ErrorCode != "TASK_EXECUTION_FAILED" || entry.Timestamp != "2025-03-04T04:30:00Z" || strings.Contains(entry.Message, "sample") {
		t.Fatal("历史事件转换错误")
	}
	if state.RuntimeLogs[1].Level != "warn" || state.RuntimeLogs[1].Message != "第一行 第二行" {
		t.Fatal("旧警告级别或换行处理错误")
	}
	long := LogSummary("密码 token=sample\n" + strings.Repeat("界", 601))
	if !utf8.ValidString(long) || len(long) > 600 || strings.Contains(long, "sample") {
		t.Fatal("摘要未按字符边界截断或含未遮蔽材料")
	}
	text := RedactLogText("第一行\n第二行 sampleValue", []string{"sampleValue"})
	if text != "第一行\n第二行 [REDACTED]" {
		t.Fatal("第三方输出原文格式有变")
	}
}

func TestProbeHitRedactsEncodedQueryWithoutChangingOtherValues(t *testing.T) {
	hit := FrpProbeHit{Path: "/probe", Method: "GET", RawQuery: "client=example&pass%77ord=test-value&x-api-key=demo-value&zone=example"}
	hit.NormalizeLog()
	if strings.Contains(hit.RawQuery, "test-value") || strings.Contains(hit.RawQuery, "demo-value") || !strings.Contains(hit.RawQuery, "client=example") || !strings.Contains(hit.RawQuery, "zone=example") || hit.Event != "probe.request" {
		t.Fatal("探针命中序列化错误")
	}
}

func TestLogErrorCodeIgnoresUnregisteredIdentifiers(t *testing.T) {
	if got := messageErrorCode("error", "EXAMPLE_TENANT_LABEL: operation unavailable"); got != "OPERATION_FAILED" {
		t.Fatal("unregistered identifier used as a log error code")
	}
}
