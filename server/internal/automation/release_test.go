package automation

import (
	"encoding/json"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestAuthorizedReleaseCanBeTrackedAndRetried(t *testing.T) {
	s := testService(t)
	ctx := t.Context()
	app, err := s.runtime.CreateObject(ctx, "applications", "app", domain.Object{"projectId": domain.BuiltinProjectID, "name": "示例应用"})
	if err != nil {
		t.Fatal(err)
	}
	id := app["id"].(string)
	serverID := "server_example"
	if err := s.runtime.Store.MutateState(ctx, func(st *domain.State) error {
		metrics := domain.Object{"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager", "swarmVerificationStatus": "verified", "swarmClusterId": "cluster_example", "swarmNodeId": "node_example", "dockerRegistryMirrors": []string{}}
		st.Servers = []domain.Object{{"id": serverID, "health": domain.Object{"metrics": metrics}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	config := domain.Object{
		"sourceConfig":              domain.Object{"provider": "image", "image": "example:1"},
		"deploymentManagerServerId": serverID,
		"swarmRuntime":              domain.Object{"services": []domain.Object{{"serverId": serverID, "slotKey": "main", "serviceName": "sample-main", "replicas": 1}}},
	}
	if _, err := s.runtime.UpsertApplicationConfig(ctx, id, config); err != nil {
		t.Fatal(err)
	}
	permissions := []Permission{{"application_release", []string{id}, ModeDirect}, {"operation_get", []string{}, ModeDirect}}
	_, token, err := s.grants.Add("发布客户端", permissions, []Rule{{"application_release", id}})
	if err != nil {
		t.Fatal(err)
	}
	call := Call{"application_release", map[string]string{"target": id, "requestKey": "one"}}
	for range 2 {
		if code, out := request(s, "/call", token, call); code != 200 {
			t.Fatalf("发布未能排队：%d %s", code, out)
		}
	}
	state, err := s.runtime.Store.LoadState(ctx)
	if err != nil || len(state.Tasks) != 1 || len(state.OperationRuns) != 1 || len(state.ReleasePlans) != 1 {
		t.Fatalf("发布未按请求键复用或未登记发布计划：%v", err)
	}
	task := state.Tasks[0]
	code, raw := request(s, "/call", token, Call{"operation_get", map[string]string{"target": task.ID}})
	var run struct{ ID, Status string }
	if code != 200 || json.Unmarshal(raw, &run) != nil || run.ID != task.ID || run.Status != task.Status || task.Status != "queued" {
		t.Fatalf("操作查询与 Worker 队列不一致：%s", raw)
	}
}
