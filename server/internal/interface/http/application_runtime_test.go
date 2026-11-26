package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestApplicationRuntimeEndpointsDistinguishDraftAndConfirmOnce(t *testing.T) {
	api := newTestAPI(t)
	if err := api.store.MutateState(context.Background(), func(state *domain.State) error {
		state.Applications = append(state.Applications, domain.Object{"id": "app_draft", "name": "草稿应用"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		req, err := http.NewRequest(method, api.server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		value := map[string]any{}
		if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, value
	}
	for _, path := range []string{
		"/projects/applications/app_draft/runtime",
		"/logs/runtime?source=service&targetKind=application&targetId=app_draft&serviceSlotKey=web",
	} {
		status, result := request(http.MethodGet, path, "")
		if status != http.StatusOK || result["availability"] != "not_deployed" || result["unavailableReason"] != "APPLICATION_DEPLOYMENT_MANAGER_REQUIRED" {
			t.Fatalf("草稿应用未报告未部署状态: %d", status)
		}
	}
	status, result := request(http.MethodPost, "/projects/applications/app_draft/uninstall", `{"removeVolumes":true}`)
	if status != http.StatusBadRequest || result["errorCode"] != "APPLICATION_VOLUME_REMOVAL_UNSUPPORTED" {
		t.Fatalf("未支持的删卷请求应被拒绝: %d", status)
	}
	status, result = request(http.MethodPost, "/projects/applications/app_draft/start", `{}`)
	if status != http.StatusOK || result["status"] != "queued" {
		t.Fatalf("创建应用操作失败: %d", status)
	}
	state, _ := api.store.LoadState(context.Background())
	if len(state.Tasks) != 1 || state.Tasks[0].Status != "pending_confirmation" {
		t.Fatal("危险操作未等待确认")
	}
	status, result = request(http.MethodPost, "/projects/applications/app_draft/stop", `{}`)
	if status != http.StatusOK || result["status"] != "queued" {
		t.Fatalf("同应用后续操作未接受排队: %d", status)
	}
}
