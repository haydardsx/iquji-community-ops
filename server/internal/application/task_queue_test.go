package application

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestTaskQueueUsesConfirmationOrderAndAllowsIndependentClaims(t *testing.T) {
	runtime, store, ctx := newTaskTestRuntime(t)
	create := func(target string) domain.Task {
		t.Helper()
		task, err := runtime.CreateTask(ctx, "restart_application", "application", target, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	first, second, independent := create("app_one"), create("app_one"), create("app_two")
	for _, task := range []domain.Task{second, first, independent} {
		if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := store.ClaimNextTask(ctx, -1)
	if err != nil || claimed == nil || claimed.ID != second.ID {
		t.Fatal("必须按确认顺序领取")
	}
	next, err := store.ClaimNextTask(ctx, -1)
	if err != nil || next == nil || next.ID != independent.ID {
		t.Fatal("无关应用必须能跳过等待项")
	}
	if blocked, err := store.ClaimNextTask(ctx, -1); err != nil || blocked != nil {
		t.Fatal("同应用不能并跑")
	}
	view, err := runtime.GetOperationRun(ctx, first.ID)
	if err != nil || objectMapFromAny(view["queue"])["blockedByOperationRunId"] != second.ID {
		t.Fatal("队列缺少等待来源")
	}
	if err := store.CompleteTask(domain.WithTaskClaim(ctx, *claimed), claimed.ID, "succeeded", "完成"); err != nil {
		t.Fatal(err)
	}
	last, err := store.ClaimNextTask(ctx, -1)
	if err != nil || last == nil || last.ID != first.ID {
		t.Fatal("前项完成后必须自动放行")
	}
}

func TestConcurrentTaskClaimsKeepResourceAndSlotLimitsAtomic(t *testing.T) {
	runtime, store, ctx := newTaskTestRuntime(t)
	for i := 0; i < 12; i++ {
		target := fmt.Sprintf("app_%d", i%6)
		if _, err := runtime.CreateTask(ctx, "refresh_application", "application", target, "示例应用", nil); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan *domain.Task, 12)
	errs := make(chan error, 12)
	var group sync.WaitGroup
	for range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			task, err := store.ClaimNextTask(ctx, -1)
			if err != nil {
				errs <- err
				return
			}
			if task != nil {
				results <- task
			}
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	count := 0
	for task := range results {
		count++
		if seen[task.TargetID] {
			t.Fatal("原子claim允许同一应用并跑")
		}
		seen[task.TargetID] = true
	}
	if count != 4 {
		t.Fatalf("并发领取超出或丢失执行槽位: %d", count)
	}
}

func TestTaskQueueCapsClaimsAndFencesOldAttempts(t *testing.T) {
	runtime, store, ctx := newTaskTestRuntime(t)
	for i := 0; i < 5; i++ {
		if _, err := runtime.CreateTask(ctx, "refresh_inventory", "server", fmt.Sprintf("srv_%d", i), "示例服务器", nil); err != nil {
			t.Fatal(err)
		}
	}
	claims := []domain.Task{}
	for i := 0; i < 4; i++ {
		task, err := store.ClaimNextTask(ctx, -1)
		if err != nil || task == nil {
			t.Fatal("领取槽位失败")
		}
		claims = append(claims, *task)
	}
	if task, err := store.ClaimNextTask(ctx, -1); err != nil || task != nil {
		t.Fatal("默认最多四个执行槽")
	}
	old := claims[0]
	if err := store.MutateState(ctx, func(state *domain.State) error {
		task := findTask(state.Tasks, old.ID)
		task.LeaseExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.ClaimNextTask(ctx, time.Minute)
	if err != nil || recovered == nil || recovered.ID != old.ID || recovered.Attempt <= old.Attempt || recovered.ClaimToken == old.ClaimToken {
		t.Fatal("旧租约必须换新执行身份")
	}
	staleCtx := domain.WithTaskClaim(ctx, old)
	if err := store.MutateState(staleCtx, func(state *domain.State) error {
		state.Projects = append(state.Projects, domain.Object{"id": "stale_write"})
		return nil
	}); err == nil || err.Error() != "TASK_LEASE_LOST" {
		t.Fatalf("旧执行写回未被拦截: %v", err)
	}
	if err := store.CompleteTask(staleCtx, old.ID, "succeeded", "旧结果"); err == nil {
		t.Fatal("旧执行终态覆盖了恢复运行")
	}
	if err := store.HeartbeatTask(domain.WithTaskClaim(ctx, *recovered), recovered.ID); err != nil {
		t.Fatal(err)
	}
}

func TestQueuedCancellationOnlyCancelsSelectedRun(t *testing.T) {
	runtime, store, ctx := newTaskTestRuntime(t)
	first, err := runtime.CreateTask(ctx, "restart_application", "application", "app_one", "示例应用", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.CreateTask(ctx, first.Action, first.TargetKind, first.TargetID, first.TargetName, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range []domain.Task{first, second} {
		if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.CancelTask(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if findTask(state.Tasks, first.ID).Status != "queued" || findTask(state.Tasks, second.ID).Status != "cancelled" {
		t.Fatal("取消影响了其他队列项")
	}
	for _, run := range state.OperationRuns {
		if run.ID == first.ID && run.Status != "queued" {
			t.Fatal("取消误改其他运行记录")
		}
	}
}

func TestTaskRequestIdempotencyReusesRunAndRejectsChangedInput(t *testing.T) {
	runtime, store, ctx := newTaskTestRuntime(t)
	requestCtx := WithTaskRequest(ctx, "request_example", "digest_one")
	first, err := runtime.CreateTask(requestCtx, "restart_application", "application", "app_one", "示例应用", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.CreateTask(requestCtx, first.Action, first.TargetKind, first.TargetID, first.TargetName, nil)
	if err != nil || first.ID != second.ID {
		t.Fatal("请求重试未复用运行")
	}
	if _, err := runtime.CreateTask(WithTaskRequest(ctx, "request_example", "digest_two"), first.Action, first.TargetKind, first.TargetID, first.TargetName, nil); err == nil || err.Error() != "IDEMPOTENCY_KEY_CONFLICT" {
		t.Fatalf("同键不同输入未拒绝: %v", err)
	}
	state, _ := store.LoadState(ctx)
	if len(state.Tasks) != 1 {
		t.Fatal("请求重试产生了重复队列项")
	}
}
