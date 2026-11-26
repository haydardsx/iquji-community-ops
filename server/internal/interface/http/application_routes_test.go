package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestApplicationRoutesUseScopedDraftAndOperationContracts(t *testing.T) {
	api := newTestAPI(t)
	if err := api.store.MutateState(context.Background(), func(state *domain.State) error {
		state.Applications = []domain.Object{{"id": "app_one"}, {"id": "app_two"}}
		state.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "app_one", "swarmRuntime": domain.Object{"services": []domain.Object{{"slotKey": "web", "serviceName": "app-web"}}}}}
		state.Domains = []domain.Object{{"id": "domain_one", "name": "app.example.test"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	base := "/projects/applications/app_one/routes"
	input := json.RawMessage(`{"domainIds":["domain_one"],"services":[{"slotKey":"web","serviceName":"app-web"}],"rules":[{"path":"/","pathHandling":"preserve"}],"disabled":false}`)
	response := api.request(t, http.MethodPost, base, input)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("草稿保存状态=%d", response.StatusCode)
	}
	route := decodeTestJSON[map[string]any](t, response)
	id, _ := route["id"].(string)
	if route["status"] != "pending" || route["lastPublishedAt"] != nil {
		t.Fatal("保存草稿提前发布")
	}
	response = api.request(t, http.MethodPut, "/projects/applications/app_two/routes/"+id, input)
	if response.StatusCode == http.StatusOK {
		t.Fatal("允许跨应用写路由")
	}
	response.Body.Close()
	response = api.request(t, http.MethodPut, base+"/"+id, json.RawMessage(`{"domains":["other.example.test"],"pathPrefix":"/","serviceName":"arbitrary"}`))
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal("未拒绝旧字段")
	}
	response.Body.Close()
	response = api.request(t, http.MethodPost, base+"/"+id+"/publish", json.RawMessage(`{}`))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("发布操作状态=%d", response.StatusCode)
	}
	operation := decodeTestJSON[map[string]any](t, response)
	if operation["action"] != "apply_route" || operation["targetId"] != id || operation["task"] != nil || operation["steps"] != nil {
		t.Fatal("没有返回轻量操作合同")
	}
	state, _ := api.store.LoadState(context.Background())
	if state.Routes[0]["status"] != "pending" || state.Tasks[0].Status != "pending_confirmation" {
		t.Fatal("未确认就改变了路由事实")
	}
}

func TestApplicationRoutesAcceptPerEntryWeightsAndRejectInvalidRatios(t *testing.T) {
	api := newTestAPI(t)
	if err := api.store.MutateState(context.Background(), func(state *domain.State) error {
		state.Applications = []domain.Object{{"id": "app_one"}}
		state.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "app_one", "swarmRuntime": domain.Object{"services": []domain.Object{
			{"slotKey": "one", "serviceName": "app-one"}, {"slotKey": "two", "serviceName": "app-two"},
		}}}}
		state.Domains = []domain.Object{{"id": "domain_one", "name": "app.example.test"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	base := "/projects/applications/app_one/routes"
	input := map[string]any{
		"domainIds": []string{"domain_one"},
		"services":  []map[string]any{{"slotKey": "one", "serviceName": "app-one", "weight": 1}, {"slotKey": "two", "serviceName": "app-two", "weight": 3}},
		"rules":     []map[string]any{{"path": "/", "pathHandling": "preserve"}}, "disabled": false,
	}
	response := api.request(t, http.MethodPost, base, input)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("入口权重保存状态=%d", response.StatusCode)
	}
	route := decodeTestJSON[map[string]any](t, response)
	services := domain.ObjectSliceFromAny(route["services"])
	if domain.NumberValue(services[0]["weight"]) != 1 || domain.NumberValue(services[1]["weight"]) != 3 {
		t.Fatal("API丢失入口绑定权重")
	}
	for _, weights := range [][2]any{{0, 0}, {-1, 1}, {1.5, 1}, {"3", 1}, {nil, 1}, {2147483648, 1}} {
		input["services"] = []map[string]any{{"slotKey": "one", "serviceName": "app-one", "weight": weights[0]}, {"slotKey": "two", "serviceName": "app-two", "weight": weights[1]}}
		response = api.request(t, http.MethodPost, base, input)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("非法入口比例 %v 没有拒绝：%d", weights, response.StatusCode)
		}
		response.Body.Close()
	}
	for _, weight := range []int{0, 7} {
		input["services"] = []map[string]any{{"slotKey": "one", "serviceName": "app-one", "weight": weight}}
		response = api.request(t, http.MethodPost, base, input)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("单服务入口保存状态=%d", response.StatusCode)
		}
		route = decodeTestJSON[map[string]any](t, response)
		if domain.NumberValue(domain.ObjectSliceFromAny(route["services"])[0]["weight"]) != 1 {
			t.Fatal("API没有把单服务入口归一为全部流量")
		}
	}
}
