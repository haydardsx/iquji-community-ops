package application

import (
	"context"
	"errors"
	"sort"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const swarmDepartureCompletedLimit = 20

func (r *Runtime) swarmDepartureSaver(serverID, operationID string) func(context.Context, domain.Object) error {
	return func(ctx context.Context, record domain.Object) error {
		next := cloneObject(record)
		// 日志入口只由任务上下文赋值，执行器不能指定其他操作的身份。
		next["operationRunId"], next["updatedAt"] = operationID, domain.Now()
		err := r.Store.MutateState(ctx, func(state *domain.State) error {
			server := findObject(state.Servers, serverID)
			if server == nil {
				return errors.New("SERVER_NOT_FOUND")
			}
			records, err := mergeSwarmDepartures(objectSlice(server["swarmDepartures"]), next)
			if err != nil {
				return err
			}
			server["swarmDepartures"] = records
			return nil
		})
		if err != nil {
			return errors.New("SWARM_DEPARTURE_RECORD_SAVE_FAILED")
		}
		return nil
	}
}

func mergeSwarmDepartures(existing []domain.Object, incoming domain.Object) ([]domain.Object, error) {
	if err := validateSwarmDeparture(incoming); err != nil {
		return nil, err
	}
	next := cloneObject(incoming)
	nodeID, clusterID := stringValue(next["nodeId"]), stringValue(next["clusterId"])
	records := make([]domain.Object, 0, len(existing)+1)
	for _, record := range existing {
		oldCluster := stringValue(record["clusterId"])
		// 非空集群身份由执行器核验；仅同节点的旧空身份记录可以补全，其他集群始终独立保留。
		matches := stringValue(record["nodeId"]) == nodeID && (oldCluster == clusterID || oldCluster == "" && clusterID != "")
		if !matches {
			records = append(records, cloneObject(record))
			continue
		}
		for _, key := range []string{"operationRunId", "departedAt"} {
			if value := stringValue(record[key]); value != "" {
				next[key] = value
			}
		}
		for _, key := range []string{"managerServerId", "managerAddress"} {
			if value := stringValue(record[key]); stringValue(next[key]) == "" && value != "" {
				next[key] = value
			}
		}
	}
	records = append(records, next)
	sort.SliceStable(records, func(i, j int) bool {
		return stringValue(records[i]["updatedAt"]) > stringValue(records[j]["updatedAt"])
	})
	kept := make([]domain.Object, 0, len(records))
	completed := 0
	for _, record := range records {
		if record["cleanupStatus"] == "removed" {
			completed++
			if completed > swarmDepartureCompletedLimit {
				continue
			}
		}
		kept = append(kept, record)
	}
	return kept, nil
}

func validateSwarmDeparture(record domain.Object) error {
	allowed := map[string]bool{"nodeId": true, "clusterId": true, "managerServerId": true, "managerAddress": true, "cleanupStatus": true, "reasonCode": true, "departedAt": true, "updatedAt": true, "operationRunId": true}
	for key, value := range record {
		text, ok := value.(string)
		if !allowed[key] || !ok || len(text) > 512 {
			return errors.New("SWARM_DEPARTURE_RECORD_SAVE_FAILED")
		}
	}
	if stringValue(record["nodeId"]) == "" {
		return errors.New("SWARM_DEPARTURE_RECORD_SAVE_FAILED")
	}
	switch record["cleanupStatus"] {
	case "leaving", "pending", "removed", "leave_failed":
	default:
		return errors.New("SWARM_DEPARTURE_RECORD_SAVE_FAILED")
	}
	if code := stringValue(record["reasonCode"]); code != "" && !domain.IsPublicDiagnosticCode(code) {
		return errors.New("SWARM_DEPARTURE_RECORD_SAVE_FAILED")
	}
	return nil
}
