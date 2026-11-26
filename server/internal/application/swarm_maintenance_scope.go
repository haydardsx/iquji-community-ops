package application

import (
	"context"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) ensureNoSwarmMaintenance(ctx context.Context, serverID string) error {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return err
	}
	for _, task := range state.Tasks {
		_, claimed := domain.TaskClaimFromContext(ctx)
		if (taskCurrentlyExecuting(task) || !claimed) && swarmMaintenanceBlocksTask(task) && swarmServersRelated(state, task.TargetID, serverID) {
			return &TaskAlreadyActiveError{ActiveTaskID: task.ID}
		}
	}
	return nil
}

func certificateUsesSwarmServer(state domain.State, certificateID, serverID string) bool {
	certificate := findObject(state.Certificates, certificateID)
	resolver := findObject(state.Resolvers, stringValue(certificate["resolverId"]))
	ingress := findObject(state.Ingresses, stringValue(resolver["ingressId"]))
	for _, candidate := range objectStringSlice(ingress["serverIds"]) {
		if swarmServersRelated(state, candidate, serverID) {
			return true
		}
	}
	return false
}
