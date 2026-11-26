package httpapi

import (
	"encoding/json"
	"net/http"
)

type applicationRouteRequest struct {
	DomainIDs []string `json:"domainIds"`
	Services  []struct {
		SlotKey     string          `json:"slotKey"`
		ServiceName string          `json:"serviceName"`
		PortName    string          `json:"portName,omitempty"`
		Weight      json.RawMessage `json:"weight,omitempty"`
	} `json:"services"`
	Rules []struct {
		Path              string `json:"path"`
		PathHandling      string `json:"pathHandling"`
		RewriteTargetPath string `json:"rewriteTargetPath,omitempty"`
	} `json:"rules"`
	Disabled *bool `json:"disabled"`
}

func (s *Server) handleApplicationRouteCollection(w http.ResponseWriter, r *http.Request, appID string, parts []string) {
	if len(parts) == 1 && r.Method == http.MethodGet {
		value, err := s.runtime.ApplicationRoutes(r.Context(), appID)
		writeResult(w, r, value, err)
		return
	}
	if (len(parts) == 1 && r.Method == http.MethodPost) || (len(parts) == 2 && r.Method == http.MethodPut) {
		var request applicationRouteRequest
		if !decodeStrict(w, r, &request) {
			return
		}
		if request.Disabled == nil {
			writeError(w, r, http.StatusBadRequest, "ROUTE_DISABLED_INVALID")
			return
		}
		input, err := requestObject(request)
		if err != nil {
			writeResult(w, r, nil, err)
			return
		}
		id := ""
		if len(parts) == 2 {
			id = parts[1]
		}
		value, err := s.runtime.UpsertApplicationRoute(r.Context(), appID, id, input)
		writeResult(w, r, value, err)
		return
	}
	if len(parts) >= 2 {
		action := ""
		switch {
		case len(parts) == 2 && r.Method == http.MethodDelete:
			action = "delete_route"
		case len(parts) == 3 && parts[2] == "publish" && r.Method == http.MethodPost:
			var request struct{}
			if !decodeStrict(w, r, &request) {
				return
			}
			action = "apply_route"
		case len(parts) == 3 && parts[2] == "status" && r.Method == http.MethodPatch:
			var request struct {
				Disabled *bool `json:"disabled"`
			}
			if !decodeStrict(w, r, &request) {
				return
			}
			if request.Disabled == nil || !*request.Disabled {
				writeError(w, r, http.StatusBadRequest, "ROUTE_DISABLE_REQUIRED")
				return
			}
			action = "disable_route"
		}
		if action != "" {
			task, err := s.runtime.CreateRouteOperation(r.Context(), appID, parts[1], action)
			s.writeOperationRun(w, r, task.ID, err)
			return
		}
	}
	writeError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND")
}
