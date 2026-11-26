package application

import (
	"errors"
	"slices"
	"sort"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type ApplicationOutputUsageError struct {
	Code         string
	Applications []domain.ResourceRef
}

func (e *ApplicationOutputUsageError) Error() string { return e.Code }

// 沿变量转发查找直接和间接消费者；响应只携带资源标识和名称。
func outputUsageError(state domain.State, code, ownerID string, outputIDs []string, includeOwner, visibilityOnly bool) error {
	pending := slices.Clone(outputIDs)
	affected := map[string]bool{}
	visited := map[string]bool{}
	if includeOwner {
		affected[ownerID] = true
	}
	for len(pending) > 0 {
		id := pending[0]
		pending = pending[1:]
		if visited[id] {
			continue
		}
		visited[id] = true
		for _, cfg := range state.ApplicationDeploymentConfigs {
			consumerID := stringValue(cfg["applicationId"])
			for _, v := range objectSlice(cfg["environmentVariables"]) {
				if v["valueSource"] != "output_ref" || v["outputId"] != id {
					continue
				}
				if visibilityOnly && slices.Contains(outputIDs, id) {
					if _, _, ok := visibleApplicationOutput(state, consumerID, id); ok {
						continue
					}
				}
				affected[consumerID] = true
				for _, next := range objectSlice(cfg["outputVariables"]) {
					if next["valueSource"] != "variable" {
						continue
					}
					ref := applicationOutputInput(cfg, next)
					if ref["valueSource"] == "output_ref" && ref["outputId"] == id {
						pending = append(pending, stringValue(next["id"]))
					}
				}
			}
		}
	}
	items := []domain.ResourceRef{}
	for _, a := range state.Applications {
		id := stringValue(a["id"])
		if affected[id] {
			items = append(items, resourceRef("application", id, stringValue(a["name"])))
		}
	}
	if len(items) == 0 {
		return nil
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return &ApplicationOutputUsageError{Code: code, Applications: items}
}

func outputSourceError(state domain.State, code, ownerID, outputID string) error {
	if err := outputUsageError(state, code, ownerID, []string{outputID}, true, false); err != nil {
		return err
	}
	return errors.New(code)
}

func validateRouteOutputDeletion(state domain.State, ownerID, routeID string) error {
	cfg := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", ownerID)
	ids := []string{}
	for _, o := range objectSlice(cfg["outputVariables"]) {
		if (o["valueSource"] == "route_url" || o["valueSource"] == "static_url") && o["routeId"] == routeID {
			ids = append(ids, stringValue(o["id"]))
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return outputUsageError(state, "APPLICATION_ROUTE_OUTPUT_IN_USE", ownerID, ids, true, false)
}
