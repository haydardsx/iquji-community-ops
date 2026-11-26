package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeStateRemovesUnusedManualApplicationLogConfiguration(t *testing.T) {
	state, err := NormalizeState([]byte(`{"applicationDeploymentConfigs":[{"applicationId":"app_one","runtimeLogConfig":{"enabled":false,"provider":"file","locator":{"path":"logs/app.log"}}}],"tasks":[{"id":"task_one","payload":{"runtimeConfigSnapshot":{"runtimeLogConfig":{"retentionDays":7},"gatewayServerId":"server_one"}}}],"releasePlans":[{"id":"plan_one","buildSnapshot":{"runtimeLogConfig":{"enabled":true}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeObject(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustApplicationLogJSON(t, encoded)), "runtimeLogConfig") {
		t.Fatal("无效手工日志配置仍暴露在正式状态中")
	}
	if ObjectFromAny(state.Tasks[0].Payload["runtimeConfigSnapshot"])["gatewayServerId"] != "server_one" {
		t.Fatal("日志迁移破坏了已有部署定位")
	}
}

func mustApplicationLogJSON(t *testing.T, value Object) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
