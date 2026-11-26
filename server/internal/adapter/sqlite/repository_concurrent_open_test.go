package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestConcurrentOpenPreservesExistingOperationalState(t *testing.T) {
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "state.sqlite")
	store, err := Open(ctx, database, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "srv_preserved", "name": "验证节点"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var group sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			opened, err := Open(ctx, database, t.TempDir())
			if err != nil {
				errs <- err
				return
			}
			defer opened.Close()
			state, err := opened.LoadState(ctx)
			if err != nil {
				errs <- err
				return
			}
			if len(state.Servers) != 1 || state.Servers[0]["id"] != "srv_preserved" {
				errs <- errors.New("并发打开覆盖了已有运行状态")
			}
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestConcurrentBootstrapDoesNotOverwriteNewQueuedTasks(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	database := filepath.Join(t.TempDir(), "state.sqlite")
	writer, err := Open(ctx, database, root)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	const count = 40
	start := make(chan struct{})
	errs := make(chan error, 4)
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		<-start
		for i := 0; i < count; i++ {
			id := fmt.Sprintf("task_%d", i)
			if err := writer.MutateState(ctx, func(state *domain.State) error {
				state.Tasks = append(state.Tasks, domain.Task{ID: id, Status: "queued", TargetKind: "server", TargetID: "server_example"})
				return nil
			}); err != nil {
				errs <- err
				return
			}
		}
	}()
	for range 3 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			for range 5 {
				opened, err := Open(ctx, database, root)
				if err != nil {
					errs <- err
					return
				}
				if err := opened.Close(); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	state, err := writer.LoadState(ctx)
	if err != nil || len(state.Tasks) != count {
		t.Fatalf("初始化覆盖了并发入队任务: count=%d err=%v", len(state.Tasks), err)
	}
	seen := map[string]bool{}
	for _, task := range state.Tasks {
		seen[task.ID] = true
	}
	for i := 0; i < count; i++ {
		if !seen[fmt.Sprintf("task_%d", i)] {
			t.Fatal("重启丢失队列项")
		}
	}
}
