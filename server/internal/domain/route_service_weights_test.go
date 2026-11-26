package domain

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRouteWeightMigrationPreservesEachExistingRatioAndRemoteFacts(t *testing.T) {
	state := EmptyState()
	state.ApplicationDeploymentConfigs = []Object{{"applicationId": "app_one", "swarmRuntime": Object{"services": []Object{
		{"slotKey": "one", "accessWeight": 1}, {"slotKey": "two", "accessWeight": 3}, {"slotKey": "disabled", "accessWeight": 0},
	}}}}
	remote := Object{"revision": "published", "services": []Object{{"slotKey": "one", "accessWeight": 1}}}
	state.Routes = []Object{
		{"applicationId": "app_one", "services": []Object{{"slotKey": "one"}, {"slotKey": "two"}, {"slotKey": "disabled"}}, "lastApplied": remote},
		{"applicationId": "app_one", "services": []Object{{"slotKey": "one", "weight": 7}, {"slotKey": "missing"}}, "pendingRemote": remote},
	}
	beforeRemote, _ := json.Marshal(remote)
	MigrateRouteServiceWeights(&state)
	first := ObjectSliceFromAny(state.Routes[0]["services"])
	second := ObjectSliceFromAny(state.Routes[1]["services"])
	if first[0]["weight"] != 1 || first[1]["weight"] != 3 || first[2]["weight"] != 0 || second[0]["weight"] != 7 || second[1]["weight"] != 1 {
		t.Fatal("入口比例迁移错误")
	}
	for _, service := range ObjectSliceFromAny(ObjectFromAny(state.ApplicationDeploymentConfigs[0]["swarmRuntime"])["services"]) {
		if _, exists := service["accessWeight"]; exists {
			t.Fatal("旧运行槽位仍保留权重")
		}
	}
	afterRemote, _ := json.Marshal(remote)
	if string(beforeRemote) != string(afterRemote) {
		t.Fatal("迁移改写了已发布或待核对的远端事实")
	}
	before, _ := json.Marshal(state)
	MigrateRouteServiceWeights(&state)
	after, _ := json.Marshal(state)
	if string(before) != string(after) {
		t.Fatal("权重迁移不幂等")
	}
	loaded, err := NormalizeState(before)
	if err != nil {
		t.Fatal(err)
	}
	if got := ObjectSliceFromAny(loaded.Routes[0]["services"]); !reflect.DeepEqual(got[1]["weight"], float64(3)) {
		t.Fatal("状态载入丢失入口权重")
	}
}

func TestNormalizeStateMigratesLegacyRouteWeightsBeforeReturning(t *testing.T) {
	state, err := NormalizeState([]byte(`{"applicationDeploymentConfigs":[{"applicationId":"app_one","swarmRuntime":{"services":[{"slotKey":"one","accessWeight":3},{"slotKey":"two","accessWeight":1}]}}],"routes":[{"applicationId":"app_one","services":[{"slotKey":"one"},{"slotKey":"two"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if NumberValue(ObjectSliceFromAny(state.Routes[0]["services"])[0]["weight"]) != 3 || ObjectSliceFromAny(ObjectFromAny(state.ApplicationDeploymentConfigs[0]["swarmRuntime"])["services"])[0]["accessWeight"] != nil {
		t.Fatal("载入状态时未完成权重迁移")
	}
}

func TestBackupWeightMigrationDoesNotMutateTheSourceState(t *testing.T) {
	state := EmptyState()
	state.ApplicationDeploymentConfigs = []Object{{"applicationId": "app_one", "swarmRuntime": Object{"services": []Object{{"slotKey": "one", "accessWeight": 3}}}}}
	state.Routes = []Object{{"applicationId": "app_one", "services": []Object{{"slotKey": "one"}}}}
	before, _ := json.Marshal(state)
	payload := BuildBackupPayload(state, "1", []BackupScope{BackupScopeProjects}, false)
	if NumberValue(ObjectSliceFromAny(payload.Projects.Routes[0]["services"])[0]["weight"]) != 1 {
		t.Fatal("备份没有迁移入口配置")
	}
	after, _ := json.Marshal(state)
	if string(before) != string(after) {
		t.Fatal("生成备份意外改写源状态")
	}
}

func TestSingleRouteWeightMigrationUsesFullTrafficAndRequiresRepublishForOldZero(t *testing.T) {
	for _, previous := range []int{0, 3} {
		state := EmptyState()
		state.ApplicationDeploymentConfigs = []Object{{"applicationId": "app_one", "swarmRuntime": Object{"services": []Object{{"slotKey": "one", "accessWeight": previous}}}}}
		remote := Object{"revision": "published", "services": []Object{{"slotKey": "one", "accessWeight": previous}}}
		state.Routes = []Object{{"applicationId": "app_one", "status": "active", "services": []Object{{"slotKey": "one"}}, "lastApplied": remote}}
		MigrateRouteServiceWeights(&state)
		if NumberValue(ObjectSliceFromAny(state.Routes[0]["services"])[0]["weight"]) != 1 {
			t.Fatal("单服务入口没有归一为全部流量")
		}
		if previous == 0 && state.Routes[0]["status"] != "pending" {
			t.Fatal("旧零权重入口迁移后没有等待重新发布")
		}
		if NumberValue(ObjectSliceFromAny(remote["services"])[0]["accessWeight"]) != previous || remote["revision"] != "published" {
			t.Fatal("迁移改写了远端事实")
		}
	}
}
