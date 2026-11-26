package application

import (
	"context"
	"errors"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 旧连接任务只供历史恢复回归构造；生产入口只创建备份重建任务。
func (r *Runtime) CreateSwarmConnectionOperation(ctx context.Context, serverID, mode, address string) (domain.Task, error) {
	mode, address = strings.ToLower(strings.TrimSpace(mode)), strings.TrimSpace(address)
	if err := ValidateSwarmConnectionInput(mode, address, true); err != nil {
		return domain.Task{}, err
	}
	task := newTask("reconfigure_docker_swarm", "server", serverID, "", domain.Object{"connectionMode": mode, "advertiseAddr": address})
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		server := findObject(state.Servers, serverID)
		if server == nil {
			return errors.New("SERVER_NOT_FOUND")
		}
		task.TargetName = stringValue(server["name"])
		if err := swarmConnectionOperationAvailable(*state, serverID, ""); err != nil {
			return err
		}
		appendTaskState(state, task)
		return nil
	})
	return task, err
}
