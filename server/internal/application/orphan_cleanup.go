package application

import (
	"context"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// CleanupOrphanMonitoringAgents 删除没有对应服务器的本地 Agent 记录。
// 该操作只处理控制面孤儿数据，不连接或修改任何远端主机。
func (r *Runtime) CleanupOrphanMonitoringAgents(ctx context.Context) (domain.Object, error) {
	removed := 0
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		servers := make(map[string]struct{}, len(state.Servers))
		for _, server := range state.Servers {
			servers[stringValue(server["id"])] = struct{}{}
		}
		kept := make([]domain.Object, 0, len(state.MonitoringAgents))
		for _, agent := range state.MonitoringAgents {
			if _, ok := servers[stringValue(agent["serverId"])]; ok {
				kept = append(kept, agent)
				continue
			}
			removed++
		}
		state.MonitoringAgents = kept
		if removed > 0 {
			state.OperationLogs = append(state.OperationLogs, newOperationLog(
				"cleanup_orphan_monitoring_agents", "monitoringAgents", "orphan", "succeeded",
				"清理没有对应服务器的 Agent 记录", "",
			))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return domain.Object{"removed": removed}, nil
}
