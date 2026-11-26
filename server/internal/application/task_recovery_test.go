package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestRecoveredJenkinsRunKeepsItsIdentityAndFrozenCommit(t *testing.T) {
	for _, changedCommit := range []bool{false, true} {
		r, task, tunnel := seededJenkinsExecution(t)
		ctx := context.Background()
		old, err := r.Store.ClaimNextTask(ctx, -1)
		if err != nil || old == nil {
			t.Fatal("首次领取失败", err)
		}
		err = r.Store.MutateState(ctx, func(st *domain.State) error {
			plan := findObject(st.ReleasePlans, stringValue(task.Payload["releasePlanId"]))
			plan["status"], plan["jenkinsRunId"], plan["commitSha"] = "jenkins_triggered", "run_previous", strings.Repeat("a", 40)
			if changedCommit {
				plan["commitSha"] = strings.Repeat("b", 40)
			}
			st.JenkinsReleaseRuns = append(st.JenkinsReleaseRuns, domain.Object{"id": "run_previous", "operationRunId": task.ID, "applicationId": "app_demo", "releasePlanId": plan["id"], "status": "triggered", "tokenHash": "sample", "callbackTokenHash": "sample"})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Store.ExpirePreviousWorkerLeases(ctx); err != nil {
			t.Fatal(err)
		}
		recovered, err := r.Store.ClaimNextTask(ctx, time.Minute)
		if err != nil || recovered == nil || recovered.Attempt <= old.Attempt {
			t.Fatal("恢复领取失败", err)
		}
		_, execErr := r.executeWorkerTask(domain.WithTaskClaim(ctx, *recovered), *recovered)
		st, err := r.Store.LoadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(st.JenkinsReleaseRuns) != 1 || tunnel.cancelled != 0 || tunnel.started || len(tunnel.events) != 0 {
			t.Fatal("接管应只查询原运行")
		}
		run := st.JenkinsReleaseRuns[0]
		if run["tokenHash"] != "" || run["callbackTokenHash"] != "" {
			t.Fatal("接管应撤销旧通道身份")
		}
		if changedCommit {
			if execErr == nil || execErr.Error() != "JENKINS_COMMIT_MISMATCH" || run["status"] != "cleanup_required" {
				t.Fatal("原提交不符，应阻断恢复及后项", execErr)
			}
			if err := r.Store.CompleteTask(domain.WithTaskClaim(ctx, *recovered), recovered.ID, "failed", "JENKINS_COMMIT_MISMATCH"); err != nil {
				t.Fatal(err)
			}
			next, err := r.CreateApplicationReleaseOperation(ctx, "app_demo", "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = r.ConfirmTask(ctx, next.ID); err != nil {
				t.Fatal(err)
			}
			if next, err := r.Store.ClaimNextTask(ctx, -1); err != nil || next != nil {
				t.Fatal("不确定的运行释放了资源锁", err)
			}
		} else if execErr != nil || run["status"] != "succeeded" {
			t.Fatal("原有远端终态未入库", execErr)
		}
		if err := r.Store.MutateState(domain.WithTaskClaim(ctx, *old), func(*domain.State) error { return nil }); err == nil {
			t.Fatal("过期 Worker 仍能写入")
		}
	}
}

func TestQueueScopeChangeFailsBeforeExecutor(t *testing.T) {
	runtime, store, ctx := newTaskTestRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "server_one", "health": domain.Object{"metrics": domain.Object{"swarmClusterId": "cluster_original"}}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	task, err := runtime.CreateTask(ctx, "install_monitoring_agent", "server", "server_one", "示例服务器", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers[0]["health"] = domain.Object{"metrics": domain.Object{"swarmClusterId": "cluster_changed"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if result, err := runtime.RunWorkerOnce(ctx); err != nil || result.Processed != 1 {
		t.Fatal("资源范围变化后工作线程应处理一项任务")
	}
	run, err := runtime.GetOperationRun(ctx, task.ID)
	if err != nil || run["status"] != "failed" || run["errorCode"] != "TASK_RESOURCE_SCOPE_CHANGED" {
		t.Fatal("资源范围变化后缺少失败状态或预期诊断码")
	}
}
