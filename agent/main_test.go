package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestReportJSONContract(t *testing.T) {
	payload, err := json.Marshal(report{
		ServerID:        "srv_test",
		Version:         "1.0.0",
		ProtocolVersion: 1,
		SampledAt:       "2026-05-14T00:00:00Z",
		Capabilities:    []string{"host", "docker", "swarm"},
		Samples: []metricSample{
			{
				TargetKind: "server",
				TargetID:   "srv_test",
				SampledAt:  "2026-05-14T00:00:00Z",
				Values: map[string]float64{
					"cpuPercent":      10,
					"memoryPercent":   20,
					"diskPercent":     30,
					"dockerDiskBytes": 40,
				},
				Labels: map[string]string{"serverId": "srv_test"},
			},
		},
		SwarmSnapshot: &swarmSnapshot{
			Nodes:    []swarmNode{},
			Services: []swarmService{},
			Tasks:    []swarmTask{},
			Networks: []swarmNetwork{},
			Summary:  "nodes=0 services=0 tasks=0",
			Status:   "success",
		},
	})
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	for _, key := range []string{
		"serverId",
		"version",
		"protocolVersion",
		"sampledAt",
		"capabilities",
		"samples",
		"swarmSnapshot",
	} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("missing report key %q", key)
		}
	}
}

func TestParseBytes(t *testing.T) {
	cases := map[string]float64{
		"1.5GB":        1500000000,
		"2 MiB / 10GB": 2097152,
		"512B":         512,
		"":             0,
	}
	for input, expected := range cases {
		if actual := parseBytes(input); actual != expected {
			t.Fatalf("parseBytes(%q) = %v, want %v", input, actual, expected)
		}
	}
}

func TestParseDockerJSONRowsTypedProjection(t *testing.T) {
	rows := parseDockerJSONRows(`
{"Name":"web","CPUPerc":"1.25%"}
not-json
{"Size":"2 MiB / 10GB"}
`)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if value := rows[0].First("Missing", "Name"); value != "web" {
		t.Fatalf("first row name = %q, want web", value)
	}
	if value := parseBytes(rows[1].String("Size")); value != 2097152 {
		t.Fatalf("second row size = %v, want 2097152", value)
	}
}

func TestAuthorizedRejectsInvalidRequests(t *testing.T) {
	cfg := config{Token: "secret"}
	handler := authorized(cfg, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	methodResponse := httptest.NewRecorder()
	handler(methodResponse, httptest.NewRequest(http.MethodPost, "/health", nil))
	if methodResponse.Code != http.StatusNotFound {
		t.Fatalf("method status = %d, want %d", methodResponse.Code, http.StatusNotFound)
	}

	tokenResponse := httptest.NewRecorder()
	handler(tokenResponse, httptest.NewRequest(http.MethodGet, "/health", nil))
	if tokenResponse.Code != http.StatusUnauthorized {
		t.Fatalf("token status = %d, want %d", tokenResponse.Code, http.StatusUnauthorized)
	}
}

func TestAuthorizedAllowsBearerToken(t *testing.T) {
	cfg := config{Token: "secret"}
	handler := authorized(cfg, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestSwarmWorkerCollectsIdentityWithoutManagerCommands(t *testing.T) {
	calls := 0
	snapshot := collectSwarmWithRunner(func(command string, args ...string) (string, error) {
		calls++
		if command != "docker" || len(args) < 1 || args[0] != "info" {
			t.Fatal("worker 不应执行 manager 专属命令")
		}
		return `{"NodeID":"worker-node","LocalNodeState":"active","ControlAvailable":false}`, nil
	})
	if calls != 1 || snapshot.NodeID != "worker-node" || snapshot.Role != "worker" || len(snapshot.Nodes) != 1 || snapshot.Status != "success" {
		t.Fatal("worker 身份采集错误")
	}
}

func TestSwarmInactiveAndFailedProbeRemainDistinct(t *testing.T) {
	inactive := collectSwarmWithRunner(func(string, ...string) (string, error) { return `{"LocalNodeState":"inactive"}`, nil })
	if inactive.Status != "not_enabled" || len(inactive.Nodes) != 0 {
		t.Fatal("未启用应中性")
	}
	failed := collectSwarmWithRunner(func(string, ...string) (string, error) { return "invalid", nil })
	if failed.Status != "error" || failed.ErrorCode != "SWARM_PROBE_FAILED" {
		t.Fatal("探测错误不应写为空集群成功")
	}
}

func TestSwarmPendingAndInvalidManagerRowsReportError(t *testing.T) {
	pending := collectSwarmWithRunner(func(string, ...string) (string, error) { return `{"LocalNodeState":"pending"}`, nil })
	if pending.Status != "error" || pending.ErrorCode != "SWARM_NODE_NOT_ACTIVE" {
		t.Fatal("待加入不能被记为未启用")
	}
	invalid := collectSwarmWithRunner(func(_ string, args ...string) (string, error) {
		if args[0] == "info" {
			return `{"LocalNodeState":"active","NodeID":"a","ControlAvailable":true,"Cluster":{"ID":"cluster"}}`, nil
		}
		return "not-json", nil
	})
	if invalid.Status != "error" || invalid.ErrorCode != "SWARM_SNAPSHOT_INVALID" {
		t.Fatal("无效行不能被记为空集群成功")
	}
}

func TestLegacyAgentConfigurationUsesProtocolV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"serverId":"example","token":"fixture"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := readConfig(path)
	if err != nil || cfg.ProtocolVersion != 1 || cfg.Port != 9137 {
		t.Fatal("兼容配置的协议和端口默认值错误", err)
	}
}
