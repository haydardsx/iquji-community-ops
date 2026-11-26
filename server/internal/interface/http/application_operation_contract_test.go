package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestApplicationActionsReturnConfirmableOperationRuns(t *testing.T) {
	for _, entry := range []struct {
		name   string
		path   string
		action string
	}{
		{"构建发布", "/projects/applications/app_release/rebuild", "request_jenkins_release"},
		{"执行发布计划", "/projects/release-plans/plan_release/execute", "request_jenkins_release"},
		{"启动", "/projects/applications/app_release/start", "start_application"},
		{"停止", "/projects/applications/app_release/stop", "stop_application"},
		{"重启", "/projects/applications/app_release/restart", "restart_application"},
		{"卸载", "/projects/applications/app_release/uninstall", "uninstall_application"},
	} {
		t.Run(entry.name, func(t *testing.T) {
			api := newTestAPI(t)
			ctx := context.Background()
			if err := api.store.MutateState(ctx, func(state *domain.State) error {
				state.Applications = []domain.Object{{"id": "app_release", "name": "发布示例"}}
				state.Servers = []domain.Object{{"id": "srv_release", "health": domain.Object{"metrics": domain.Object{
					"ssh": true, "docker": true, "jenkins": true, "jenkinsBundleVersion": "v12-jenkins-2.568.1", "swarm": true, "traefik": true,
					"swarmRole": "manager", "swarmVerificationStatus": "verified", "swarmClusterId": "cluster_example",
				}}}}
				state.ApplicationDeploymentConfigs = []domain.Object{{
					"applicationId": "app_release", "buildServerId": "srv_release", "deploymentManagerServerId": "srv_release",
					"sourceConfig": domain.Object{
						"provider": "git", "repositoryUrl": "https://example.com/demo.git", "ref": "main",
						"refType": "branch", "sourcePath": ".", "jenkinsfilePath": "Jenkinsfile",
					},
					"swarmRuntime": domain.Object{"services": []domain.Object{{"serverId": "srv_release", "slotKey": "web", "serviceName": "demo-web"}}},
				}}
				cfg := state.ApplicationDeploymentConfigs[0]
				state.ReleasePlans = []domain.Object{{"id": "plan_release", "applicationId": "app_release", "status": "queued",
					"buildServerId": cfg["buildServerId"], "deploymentManagerServerId": cfg["deploymentManagerServerId"],
					"buildSnapshot": domain.Object{"sourceSnapshot": cfg["sourceConfig"], "swarmRuntime": cfg["swarmRuntime"]},
				}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			response := api.request(t, http.MethodPost, entry.path, map[string]any{})
			operation := decodeTestJSON[domain.OperationRun](t, response)
			if response.StatusCode != http.StatusOK || operation.ID == "" || operation.Action != entry.action || operation.Status != "queued" || operation.TargetKind != "application" || operation.TargetID != "app_release" {
				t.Fatalf("应用动作未返回预期的待确认操作，HTTP %d", response.StatusCode)
			}
			state, err := api.store.LoadState(ctx)
			if err != nil || len(state.Tasks) != 1 || state.Tasks[0].Status != "pending_confirmation" {
				t.Fatal("创建任务后缺少待确认状态")
			}

			base := "/operations/runs/" + operation.ID
			response = api.request(t, http.MethodPost, base+"/confirm", nil)
			confirmed := decodeTestJSON[domain.OperationRun](t, response)
			if response.StatusCode != http.StatusOK || confirmed.ID != operation.ID || confirmed.Action != entry.action || confirmed.TargetKind != operation.TargetKind || confirmed.TargetID != operation.TargetID {
				t.Fatalf("确认后操作或目标不匹配，HTTP %d", response.StatusCode)
			}
			state, err = api.store.LoadState(ctx)
			if err != nil || len(state.Tasks) != 1 || state.Tasks[0].Status != "queued" {
				t.Fatal("确认任务后缺少排队状态")
			}

			response = api.request(t, http.MethodPost, base+"/cancel", nil)
			cancelled := decodeTestJSON[domain.OperationRun](t, response)
			if response.StatusCode != http.StatusOK || cancelled.ID != operation.ID || cancelled.Status != "cancelled" || cancelled.TargetKind != operation.TargetKind || cancelled.TargetID != operation.TargetID {
				t.Fatalf("取消后操作、目标或状态不匹配，HTTP %d", response.StatusCode)
			}
		})
	}
}
