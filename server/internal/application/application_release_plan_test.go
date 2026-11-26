package application

import (
	"context"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestReleasePlanKeepsOnlyRequestedRefUntilResolvedAndExecutesOnce(t *testing.T) {
	ctx := context.Background()
	r, _, _ := seededJenkinsExecution(t)
	request := domain.Object{"ref": "release", "refType": "tag"}
	preview, err := r.ReleasePlanPreview(ctx, "app_demo", request)
	if err != nil {
		t.Fatal(err)
	}
	summary := objectMapFromAny(preview["releasePlan"])
	if summary["ref"] != "release" || summary["refType"] != "tag" || summary["commitSha"] != nil || summary["resolvedRef"] != nil || summary["buildSnapshot"] != nil {
		t.Fatal("预检必须返回请求的版本和可公开的信息")
	}
	if _, err := r.CreateReleasePlan(ctx, "app_demo", domain.Object{"refType": "unknown"}); err == nil {
		t.Fatal("创建计划未校验请求中的 refType")
	}
	created, err := r.CreateReleasePlan(ctx, "app_demo", request)
	if err != nil {
		t.Fatal(err)
	}
	if created["provider"] != "git" || created["configSnapshot"] != nil || created["commitSha"] != nil {
		t.Fatal("创建计划返回了内部快照或未经核验的版本")
	}
	id := stringValue(created["id"])
	var manager any
	if err := r.Store.MutateState(ctx, func(state *domain.State) error {
		plan := findObject(state.ReleasePlans, id)
		source := objectMapFromAny(objectMapFromAny(plan["buildSnapshot"])["sourceSnapshot"])
		if source["ref"] != "release" || source["commitSha"] != nil || plan["artifactManifest"] != nil {
			t.Fatal("发布快照保存的版本与请求不符")
		}
		// 排队时使用计划中冻结的来源和节点。
		config := state.ApplicationDeploymentConfigs[0]
		manager = config["deploymentManagerServerId"]
		config["deploymentManagerServerId"] = "unavailable"
		config["sourceConfig"] = domain.Object{"provider": "image", "image": "invalid"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	requestCtx := WithTaskRequest(ctx, "example-operation", "example-digest")
	if _, err := r.CreateApplicationReleaseOperation(requestCtx, "", id); err == nil || err.Error() != "RELEASE_PLAN_PREVIEW_BLOCKED" {
		t.Fatal("应用迁移后仍允许排队发往原管理节点", err)
	}
	if err := r.Store.MutateState(ctx, func(state *domain.State) error {
		state.ApplicationDeploymentConfigs[0]["deploymentManagerServerId"] = manager
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	first, err := r.CreateApplicationReleaseOperation(requestCtx, "", id)
	if err != nil || first.Action != string(domain.TaskActionRequestJenkinsRelease) {
		t.Fatalf("未采用冻结的发布配置: %v", err)
	}
	if err := r.DeleteObject(ctx, "release_plans", id); err == nil {
		t.Fatal("已绑定任务的发布快照被删除")
	}
	replay, err := r.CreateApplicationReleaseOperation(requestCtx, "", id)
	if err != nil || replay.ID != first.ID {
		t.Fatalf("重试未返回同一任务: %v", err)
	}
	if _, err := r.CreateApplicationReleaseOperation(ctx, "", id); err == nil || err.Error() != "RELEASE_PLAN_STATE_INVALID" {
		t.Fatalf("为同一发布计划重复排队: %v", err)
	}
}
