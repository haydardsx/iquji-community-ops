package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmAdvertiseAddressValidationAndOperationSnapshot(t *testing.T) {
	for _, address := range []string{"192.0.2.24", "2001:db8::24", "192.0.2.24; touch /tmp/invalid", "host.example.test", "192.0.2.24:2377"} {
		t.Run(address, func(t *testing.T) {
			api := newTestAPI(t)
			if err := api.store.MutateState(context.Background(), func(state *domain.State) error { state.Servers = []domain.Object{{"id": "srv_one"}}; return nil }); err != nil {
				t.Fatal(err)
			}
			response := api.request(t, http.MethodPost, "/servers/srv_one/swarm/install", map[string]any{"mode": "worker", "managerServerId": "srv_manager", "advertiseAddr": address})
			defer response.Body.Close()
			valid := address == "192.0.2.24" || address == "2001:db8::24"
			if !valid {
				if response.StatusCode != http.StatusBadRequest {
					t.Fatalf("无效通告地址未被拒绝：%d", response.StatusCode)
				}
				return
			}
			if response.StatusCode != http.StatusOK {
				t.Fatalf("有效通告地址被拒绝：%d", response.StatusCode)
			}
			state, err := api.store.LoadState(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(state.Tasks) != 1 || state.Tasks[0].Payload["advertiseAddr"] != address || state.Tasks[0].Payload["managerServerId"] != "srv_manager" {
				t.Fatal("操作快照未保存网络选择")
			}
		})
	}
}
