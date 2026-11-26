package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmRebuildEndpointValidatesBeforeCreatingConfirmation(t *testing.T) {
	api := newTestAPI(t)
	if err := api.store.MutateState(context.Background(), func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "server_example", "name": "示例管理节点"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	private := netip.MustParsePrefix("10.0.0.0/8").Addr().Next()
	response := api.request(t, http.MethodPost, "/servers/server_example/swarm/rebuild", map[string]any{"connectionMode": "public", "advertiseAddr": private.String()})
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("公网方式接受了私网通告地址：%d", response.StatusCode)
	}
	state, err := api.store.LoadState(context.Background())
	if err != nil || len(state.Tasks) != 0 {
		t.Fatal("无效请求创建了操作")
	}
	response = api.request(t, http.MethodPost, "/servers/server_example/swarm/rebuild", map[string]any{"connectionMode": "public", "advertiseAddr": "192.0.2.10"})
	defer response.Body.Close()
	var operation domain.OperationRun
	if err := json.NewDecoder(response.Body).Decode(&operation); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || operation.Action != "rebuild_docker_swarm" || operation.ID == "" {
		t.Fatalf("备份重建未返回轻量操作：%d %#v", response.StatusCode, operation)
	}
	state, err = api.store.LoadState(context.Background())
	if err != nil || len(state.Tasks) != 1 || state.Tasks[0].Status != "pending_confirmation" || len(state.ConfirmationTokens) != 1 {
		t.Fatal("备份重建跳过危险操作确认")
	}
}

func TestSwarmConnectionCreationEndpointWasRemoved(t *testing.T) {
	api := newTestAPI(t)
	response := api.request(t, http.MethodPost, "/servers/server_example/swarm/connection", map[string]any{"connectionMode": "public", "advertiseAddr": "192.0.2.10"})
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("旧迁移创建入口仍可调用：%d", response.StatusCode)
	}
	state, err := api.store.LoadState(context.Background())
	if err != nil || len(state.Tasks) != 0 {
		t.Fatal("旧迁移入口创建了任务")
	}
}

func TestSwarmRebuildEndpointRejectsUnknownFields(t *testing.T) {
	api := newTestAPI(t)
	response := api.request(t, http.MethodPost, "/servers/server_example/swarm/rebuild", map[string]any{"connectionMode": "public", "advertiseAddr": "192.0.2.10", "force": true})
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("重建入口接受了未知字段：%d", response.StatusCode)
	}
	state, err := api.store.LoadState(context.Background())
	if err != nil || len(state.Tasks) != 0 {
		t.Fatal("无效重建请求创建了任务")
	}
}
