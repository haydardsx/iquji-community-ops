package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestDeploymentEligibilityIncludesUnavailableProjectServers(t *testing.T) {
	api := newTestAPI(t)
	if err := api.store.MutateState(context.Background(), func(state *domain.State) error {
		state.Applications = []domain.Object{{"id": "app_one", "projectId": "project_one"}}
		state.Servers = []domain.Object{
			{"id": "gateway", "health": domain.Object{"metrics": domain.Object{"ssh": true, "docker": true, "swarm": true, "traefik": true, "swarmRole": "manager", "swarmClusterId": "one", "swarmVerificationStatus": "verified"}}},
			{"id": "worker", "health": domain.Object{"metrics": domain.Object{"ssh": true, "docker": true, "swarm": true, "swarmRole": "worker", "swarmClusterId": "one", "swarmVerificationStatus": "verified"}}},
			{"id": "uninstalled", "health": domain.Object{"metrics": domain.Object{"ssh": true, "docker": true, "swarm": false, "swarmStatus": "inactive"}}},
			{"id": "other_cluster", "health": domain.Object{"metrics": domain.Object{"ssh": true, "docker": true, "swarm": true, "swarmClusterId": "two", "swarmVerificationStatus": "verified"}}},
			{"id": "other_project", "projectIds": []string{"project_other"}},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	response := api.request(t, http.MethodGet, "/projects/applications/app_one/deployment-eligibility?deploymentManagerServerId=gateway", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("资格接口返回错误：%d", response.StatusCode)
	}
	var payload struct {
		GatewayClusterID string `json:"deploymentClusterId"`
		Servers          []struct {
			ServerID string `json:"serverId"`
			Eligible bool   `json:"eligible"`
			Reasons  []struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"reasons"`
		} `json:"servers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.GatewayClusterID != "one" || len(payload.Servers) != 4 {
		t.Fatal("候选集群或项目服务器数量错误")
	}
	for _, server := range payload.Servers {
		switch server.ServerID {
		case "gateway", "worker":
			if !server.Eligible || len(server.Reasons) != 0 {
				t.Fatal("同群节点不可选")
			}
		case "uninstalled", "other_cluster":
			if server.Eligible || len(server.Reasons) == 0 {
				t.Fatal("不可用节点缺少原因")
			}
			for _, reason := range server.Reasons {
				if reason.Message == "" || server.ServerID == "uninstalled" && reason.Code == "SWARM_CLUSTER_MISMATCH" {
					t.Fatal("资格原因错误")
				}
			}
		}
	}
}

func TestSwarmUninstallImpactReadDoesNotCreateAnOperation(t *testing.T) {
	api := newTestAPI(t)
	if err := api.store.MutateState(context.Background(), func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "srv_one", "health": domain.Object{"metrics": domain.Object{"traefik": true}}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	response := api.request(t, http.MethodGet, "/servers/srv_one/swarm/uninstall-impact", nil)
	defer response.Body.Close()
	var payload struct {
		Impact struct {
			Blocked bool `json:"blocked"`
		} `json:"impact"`
		ErrorCode string `json:"errorCode"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !payload.Impact.Blocked || payload.ErrorCode != "SWARM_UNINSTALL_BLOCKED_TRAEFIK" {
		t.Fatal("迁移影响查询未返回阻断原因")
	}
	state, err := api.store.LoadState(context.Background())
	if err != nil || len(state.Tasks) != 0 {
		t.Fatalf("只读影响查询创建了操作：%v", err)
	}
}

func TestSwarmUnfinishedJoinCanBePreviewedCreatedAndConfirmedWithIndependentDependencies(t *testing.T) {
	api := newTestAPI(t)
	if err := api.store.MutateState(context.Background(), func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "srv_build", "health": domain.Object{"metrics": domain.Object{
			"docker": true, "jenkins": true, "swarmStatus": "error", "swarmRole": "none",
		}}}}
		state.ApplicationDeploymentConfigs = []domain.Object{{"id": "config_one", "buildServerId": "srv_build"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	preview := api.request(t, http.MethodGet, "/servers/srv_build/swarm/uninstall-impact", nil)
	payload := decodeTestJSON[struct {
		Impact struct {
			Blocked bool            `json:"blocked"`
			Reasons []domain.Object `json:"reasons"`
		} `json:"impact"`
		ErrorCode string `json:"errorCode"`
	}](t, preview)
	if preview.StatusCode != http.StatusOK || payload.Impact.Blocked || len(payload.Impact.Reasons) != 0 || payload.ErrorCode != "" {
		t.Fatal("退出失败加入仍被构建或 Compose 依赖阻断")
	}
	created := api.request(t, http.MethodPost, "/servers/srv_build/swarm/uninstall", json.RawMessage(`{}`))
	operation := decodeTestJSON[domain.OperationRun](t, created)
	if created.StatusCode != http.StatusOK || operation.ID == "" {
		t.Fatal("退出失败加入无法创建待确认操作")
	}
	confirmed := api.request(t, http.MethodPost, "/operations/runs/"+operation.ID+"/confirm", nil)
	defer confirmed.Body.Close()
	if confirmed.StatusCode != http.StatusOK {
		t.Fatalf("退出操作无法确认：%d", confirmed.StatusCode)
	}
}
