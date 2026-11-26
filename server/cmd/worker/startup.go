package main

import (
	"context"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

// openWorkerState 先证明旧正式 Worker 已退出，再过期旧租约；普通 Store 打开不触发抢占。
func openWorkerState(ctx context.Context, config platform.Config) (*sqlite.Store, func(), error) {
	release, err := platform.AcquireWorkerLock(config.StateDir)
	if err != nil {
		return nil, nil, err
	}
	store, err := sqlite.Open(ctx, config.DatabasePath, config.RepositoryRoot)
	if err != nil {
		release()
		return nil, nil, err
	}
	if err := store.ExpirePreviousWorkerLeases(ctx); err != nil {
		_ = store.Close()
		release()
		return nil, nil, err
	}
	return store, release, nil
}
