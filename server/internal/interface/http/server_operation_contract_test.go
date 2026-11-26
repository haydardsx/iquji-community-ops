package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestServerActionsReturnConfirmableOperationRuns(t *testing.T) {
	api := newTestAPI(t)
	if err := api.store.MutateState(context.Background(), func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "srv_one", "name": "One"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/servers/srv_one/docker/install", "/servers/srv_one/swarm/install", "/monitoring/agents/install"} {
		body := `{}`
		if strings.Contains(path, "swarm") {
			body = `{"mode":"manager"}`
		} else if strings.Contains(path, "monitoring") {
			body = `{"serverId":"srv_one"}`
		}
		response := api.request(t, http.MethodPost, path, json.RawMessage(body))
		if response.StatusCode != http.StatusOK {
			t.Fatalf("action HTTP status = %d", response.StatusCode)
		}
		operation := decodeTestJSON[map[string]any](t, response)
		if operation["id"] == "" || operation["status"] != "queued" || operation["payload"] != nil || operation["steps"] != nil || operation["confirmation"] != nil {
			t.Fatal("action did not return the compact operation contract")
		}
		confirmed := api.request(t, http.MethodPost, "/operations/runs/"+operation["id"].(string)+"/confirm", nil)
		if confirmed.StatusCode != http.StatusOK {
			t.Fatalf("operation could not be confirmed: %d", confirmed.StatusCode)
		}
		confirmed.Body.Close()
	}
}

func TestProductionRouterExcludesTestReset(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/test/reset", nil)
	if _, pattern := New(nil).mux.Handler(request); pattern != "" {
		t.Fatal("production router matched a test reset endpoint")
	}
}

func TestRetiredEnvironmentRoutes(t *testing.T) {
	api := newTestAPI(t)
	for _, path := range []string{"/environments/catalog", "/environments/installations", "/environments/installations/example/domain-binding"} {
		res := api.request(t, http.MethodPost, path, json.RawMessage(`{}`))
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("旧入口 %s 仍然可达：%d", path, res.StatusCode)
		}
	}
}
