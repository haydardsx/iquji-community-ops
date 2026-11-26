package application

import (
	"context"
	"errors"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type jenkinsAccessTestEndpoint struct {
	published, removed    int
	publishErr, removeErr error
	viewer                domain.Object
}

func (j *jenkinsAccessTestEndpoint) close() {}

func (j *jenkinsAccessTestEndpoint) publish(context.Context, string, string) error {
	j.published++
	return j.publishErr
}

func (j *jenkinsAccessTestEndpoint) remove(context.Context) error {
	j.removed++
	return j.removeErr
}

func (j *jenkinsAccessTestEndpoint) broker(_ context.Context, action string, input domain.Object) (domain.Object, error) {
	if action != "access-viewer" {
		return nil, errors.New("未知的测试操作")
	}
	j.viewer = input
	return domain.Object{}, nil
}

func TestJenkinsAccessRollbackRetirementAndUnbind(t *testing.T) {
	r, store, ctx := newTaskTestRuntime(t)
	if err := store.MutateState(ctx, seedJenkinsAccessTest); err != nil {
		t.Fatal(err)
	}
	endpoints := map[string]*jenkinsAccessTestEndpoint{}
	open := func(_ context.Context, _ domain.State, _ string, snap domain.Object, _ domain.Task) (jenkinsAccessEndpoint, error) {
		return endpoints[stringValue(snap["bindingId"])], nil
	}
	bind := func(password string) domain.Task {
		t.Helper()
		task, err := r.CreateJenkinsAccess(ctx, "build_demo", JenkinsAccessInput{"domain_demo", "gateway_demo", password}, false)
		if err != nil {
			t.Fatal(err)
		}
		endpoints[task.ID] = &jenkinsAccessTestEndpoint{}
		return task
	}
	current := func() domain.Object {
		t.Helper()
		state, err := store.LoadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return objectMapFromAny(state.Servers[0]["jenkinsAccess"])
	}
	oldPassword := "example-old-password"
	first := bind(oldPassword)
	if err := r.changeJenkinsAccessWith(ctx, first, open); err != nil {
		t.Fatal(err)
	}
	prev := objectMapFromAny(current()["applied"])
	oldRef := stringValue(prev["viewerSecretRef"])

	second := bind("example-new-password")
	candidate := endpoints[second.ID]
	candidate.publishErr = errors.New("候选入口未能验证")
	if err := r.changeJenkinsAccessWith(ctx, second, open); !errors.Is(err, candidate.publishErr) {
		t.Fatal(err)
	}
	access := current()
	prior := endpoints[first.ID]
	if len(objectMapFromAny(access["pending"])) != 0 || objectMapFromAny(access["applied"])["bindingId"] != first.ID || candidate.removed != 1 || prior.removed != 0 || prior.viewer["password"] != oldPassword || prior.viewer["url"] != prev["url"] {
		t.Fatal("改绑失败后未恢复旧入口与只读凭据")
	}

	candidate.publishErr = nil
	prior.removeErr = errors.New("旧入口回收暂时失败")
	if err := r.changeJenkinsAccessWith(ctx, second, open); !errors.Is(err, prior.removeErr) {
		t.Fatal(err)
	}
	access = current()
	snap := objectMapFromAny(access["applied"])
	if snap["bindingId"] != second.ID || objectMapFromAny(access["retiring"])["bindingId"] != first.ID || objectMapFromAny(access["retiring"])["viewerSecretRef"] != nil {
		t.Fatal("旧入口回收失败后丢失了新入口或旧入口清理进度")
	}
	if saved, err := store.HasSecretMaterial(ctx, oldRef); err != nil || saved {
		t.Fatal("保留了已轮换的凭据", err)
	}
	prior.removeErr = nil
	if err := r.changeJenkinsAccessWith(ctx, second, open); err != nil {
		t.Fatal(err)
	}
	if current()["retiring"] != nil || candidate.published != 2 || prior.removed != 2 {
		t.Fatal("重试重新发布了已生效的入口或漏回收旧入口")
	}

	unbind := newTask(string(domain.TaskActionUnbindJenkinsAccess), "server", "build_demo", "Jenkins", nil)
	if err := r.changeJenkinsAccessWith(ctx, unbind, open); err != nil {
		t.Fatal(err)
	}
	if len(current()) != 0 || candidate.removed != 2 {
		t.Fatal("解绑后仍有访问入口")
	}
	if saved, err := store.HasSecretMaterial(ctx, stringValue(snap["viewerSecretRef"])); err != nil || saved {
		t.Fatal("解绑后凭据仍存在", err)
	}
}
