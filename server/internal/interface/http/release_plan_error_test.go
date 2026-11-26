package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/application"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestWriteResultReturnsStructuredReleasePreviewBlockers(t *testing.T) {
	err := &application.ReleasePlanPreviewBlockedError{
		Blockers: []string{"SWARM_CLUSTER_MISMATCH"},
		Checks: []domain.CheckItem{{
			Code: "SWARM_CLUSTER_MISMATCH", Blocking: true, Outcome: domain.CheckOutcomeFailed,
			Target:  domain.Resource("server", "srv_worker", "工作节点"),
			Details: domain.Object{"gatewayServerName": "网关", "gatewayClusterId": "cluster_one", "serverClusterId": "cluster_two"},
		}},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/projects/applications/app_one/release-plans", nil)
	writeResult(recorder, request, nil, err)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
	}
	var payload map[string]any
	if decodeErr := json.NewDecoder(recorder.Body).Decode(&payload); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if payload["errorCode"] != "RELEASE_PLAN_PREVIEW_BLOCKED" {
		t.Fatal("stable error code missing")
	}
	details, ok := payload["details"].(map[string]any)
	if !ok || len(details["blockers"].([]any)) != 1 || len(details["checks"].([]any)) != 1 {
		t.Fatal("structured blocker details missing")
	}
}

func TestWriteResultReturnsOnlyPublicErrorDetails(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"EXAMPLE_TENANT_LABEL", "OPERATION_FAILED"},
		{"open /example/missing: file unavailable", "OPERATION_FAILED"},
		{"BACKUP_FILE_MISSING:example/archive", "BACKUP_FILE_MISSING"},
		{"FRP_TUNNEL_INVALID:FRP_TUNNEL_LOCAL_PORT_INVALID,EXAMPLE_TENANT_LABEL,LOCAL_TCP_PORT_UNREACHABLE:example", "FRP_TUNNEL_INVALID:FRP_TUNNEL_LOCAL_PORT_INVALID,LOCAL_TCP_PORT_UNREACHABLE"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		writeResult(recorder, request, nil, errors.New(test.input))
		var payload map[string]any
		if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
			t.Fatal("invalid error response")
		}
		if payload["errorCode"] != test.want || payload["message"] != test.want {
			t.Fatal("unexpected error response")
		}
	}
}
