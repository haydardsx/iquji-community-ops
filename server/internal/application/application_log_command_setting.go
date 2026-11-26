package application

import (
	"context"
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// SetApplicationLogCommand 仅更新所选应用的日志命令，并在同一事务中保留其它运行配置。
func (r *Runtime) SetApplicationLogCommand(ctx context.Context, id string, argv []string) (domain.Object, error) {
	if len(argv) > 0 {
		if _, ok := stringArguments(argv); !ok {
			return nil, errors.New("APPLICATION_CONTAINER_COMMAND_INVALID")
		}
	}
	err := r.Store.MutateState(ctx, func(s *domain.State) error {
		if findObject(s.Applications, id) == nil {
			return errors.New("APPLICATION_NOT_FOUND")
		}
		cfg := findObjectBy(s.ApplicationDeploymentConfigs, "applicationId", id)
		if cfg == nil {
			return errors.New("APPLICATION_DEPLOYMENT_CONFIG_REQUIRED")
		}
		if applicationOperationActive(*s, id) != nil {
			return errors.New("APPLICATION_OPERATION_ACTIVE")
		}
		swarm := objectMapFromAny(cfg["swarmRuntime"])
		if len(argv) == 0 {
			delete(swarm, "logCommand")
		} else {
			swarm["logCommand"] = argv
		}
		cfg["swarmRuntime"] = swarm
		cfg["updatedAt"] = domain.Now()
		s.OperationLogs = append(s.OperationLogs, newOperationLog("update_application_deployment_config", "application", id, "succeeded", "应用日志查询命令已保存", ""))
		return nil
	})
	return domain.Object{"applicationId": id, "configured": len(argv) > 0}, err
}
