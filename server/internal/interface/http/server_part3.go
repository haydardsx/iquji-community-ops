package httpapi

import (
	"net"
	"net/http"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/application"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (s *Server) handleReleasePlanRoutes(w http.ResponseWriter, r *http.Request, parts []string) {
	id := parts[0]
	if len(parts) == 2 && parts[1] == "execute" && r.Method == http.MethodPost {
		var request struct{}
		if r.ContentLength != 0 && !decodeStrict(w, r, &request) {
			return
		}
		task, err := s.runtime.CreateApplicationReleaseOperation(r.Context(), "", id)
		s.writeOperationRun(w, r, task.ID, err)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodDelete {
		err := s.runtime.DeleteObject(r.Context(), "release_plans", id)
		writeResult(w, r, map[string]any{"ok": true}, err)
		return
	}
	writeError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND")
}

func isApplicationAction(leaf string) bool {
	switch leaf {
	case "rebuild", "start", "restart", "stop", "uninstall", "uninstall/preview":
		return true
	}
	return false
}

func (s *Server) handleServersNested(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/servers/")
	id, rest := splitID(path)
	if (rest == "connection" || strings.HasPrefix(rest, "connection/")) && !terminalOrigin(r) {
		writeError(w, r, http.StatusForbidden, "SERVER_CONNECTION_ORIGIN_DENIED")
		return
	}
	switch {
	case rest == "jenkins/access":
		if r.Method == http.MethodGet {
			value, err := s.runtime.JenkinsAccess(r.Context(), id)
			writeResult(w, r, value, err)
			return
		}
		if r.Method != http.MethodPost && r.Method != http.MethodDelete {
			writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
			return
		}
		if !terminalOrigin(r) {
			writeError(w, r, http.StatusForbidden, "SERVER_CONNECTION_ORIGIN_DENIED")
			return
		}
		var input application.JenkinsAccessInput
		if r.Method == http.MethodPost && !decodeStrict(w, r, &input) {
			return
		}
		task, err := s.runtime.CreateJenkinsAccess(r.Context(), id, input, r.Method == http.MethodDelete)
		s.writeOperationRun(w, r, task.ID, err)
	case rest == "connection" && r.Method == http.MethodPut:
		var input application.ServerConnectionInput
		if !decodeStrict(w, r, &input) {
			return
		}
		value, err := s.runtime.SaveServerConnection(r.Context(), id, input)
		writeResult(w, r, value, err)
	case (rest == "connection/test" || rest == "connection/trust") && r.Method == http.MethodPost:
		var expected *string
		if rest == "connection/trust" {
			var input struct {
				Fingerprint string `json:"fingerprint"`
			}
			if !decodeStrict(w, r, &input) {
				return
			}
			expected = &input.Fingerprint
		} else {
			var input struct{}
			if !decodeStrict(w, r, &input) {
				return
			}
		}
		value, err := s.runtime.CheckServerConnection(r.Context(), id, expected)
		writeResult(w, r, value, err)
	case rest == "" && r.Method == http.MethodPut:
		var input domain.Object
		if !decode(w, r, &input) {
			return
		}
		value, err := s.runtime.UpdateObject(r.Context(), "servers", id, input)
		writeResult(w, r, value, err)
	case rest == "" && r.Method == http.MethodDelete:
		err := s.runtime.DeleteObject(r.Context(), "servers", id)
		writeResult(w, r, map[string]any{"ok": true}, err)
	case rest == "health" && r.Method == http.MethodGet:
		value, err := s.runtime.ServerHealth(r.Context(), id)
		writeResult(w, r, value, err)
	case rest == "resources" && r.Method == http.MethodGet:
		value, err := s.runtime.ServerResources(r.Context(), id)
		writeResult(w, r, value, err)
	case rest == "platform-profile" && r.Method == http.MethodPut:
		var input domain.Object
		if !decode(w, r, &input) {
			return
		}
		value, err := s.runtime.UpdateObject(r.Context(), "servers", id, domain.Object{"platformProfile": input})
		writeResult(w, r, value, err)
	case rest == "runtime/probe" && r.Method == http.MethodPost:
		value, err := s.runtime.ServerRuntimeProbe(r.Context(), id)
		writeResult(w, r, value, err)
	case rest == "roles" && r.Method == http.MethodPut:
		var input domain.Object
		if !decode(w, r, &input) {
			return
		}
		roles := objectStringSliceHTTP(input["roles"])
		value, err := s.runtime.UpdateObject(r.Context(), "servers", id, domain.Object{"roles": roles})
		writeResult(w, r, value, err)
	case rest == "secret-assets" && r.Method == http.MethodPut:
		var input domain.Object
		if !decode(w, r, &input) {
			return
		}
		result, err := s.runtime.UpdateServerSecretAssets(r.Context(), id, objectStringSliceHTTP(input["secretAssetIds"]))
		if err != nil {
			writeResult(w, r, nil, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"server": result["server"]})
	case rest == "swarm/uninstall-impact" && r.Method == http.MethodGet:
		impact, err := s.runtime.ServerRuntimeUninstallImpact(r.Context(), id, "swarm")
		if err != nil && (impact == nil || err.Error() == "SERVER_NOT_FOUND") {
			writeResult(w, r, nil, err)
			return
		}
		value := domain.Object{"impact": impact}
		if err != nil {
			value["errorCode"] = publicErrorMessage(err.Error())
		}
		writeJSON(w, http.StatusOK, value)
	case rest == "swarm/rebuild/recover" && r.Method == http.MethodPost:
		var input struct {
			OperationRunID string `json:"operationRunId"`
		}
		if !decodeStrict(w, r, &input) {
			return
		}
		task, err := s.runtime.CreateSwarmRebuildRecoveryOperation(r.Context(), id, input.OperationRunID)
		s.writeOperationRun(w, r, task.ID, err)
	case rest == "swarm/rebuild" && r.Method == http.MethodPost:
		var input struct {
			ConnectionMode string `json:"connectionMode"`
			AdvertiseAddr  string `json:"advertiseAddr"`
		}
		if !decodeStrict(w, r, &input) {
			return
		}
		task, err := s.runtime.CreateSwarmRebuildOperation(r.Context(), id, input.ConnectionMode, input.AdvertiseAddr)
		s.writeOperationRun(w, r, task.ID, err)
	case rest == "swarm/install" && r.Method == http.MethodPost:
		var input serverSwarmInstallRequest
		if !decodeStrict(w, r, &input) {
			return
		}
		input.AdvertiseAddr = strings.TrimSpace(input.AdvertiseAddr)
		if err := application.ValidateSwarmConnectionInput(input.ConnectionMode, input.AdvertiseAddr, false); err != nil {
			writeError(w, r, http.StatusBadRequest, err.Error())
			return
		}
		if input.AdvertiseAddr != "" && net.ParseIP(input.AdvertiseAddr) == nil {
			writeError(w, r, http.StatusBadRequest, "SWARM_ADVERTISE_ADDRESS_INVALID")
			return
		}
		input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
		if input.Mode != "manager" && input.Mode != "worker" {
			writeError(w, r, http.StatusBadRequest, "SWARM_MODE_INVALID")
			return
		}
		if input.Mode == "worker" && strings.TrimSpace(input.ManagerServerID) == "" {
			writeError(w, r, http.StatusBadRequest, "SWARM_MANAGER_REQUIRED")
			return
		}
		if input.Mode == "manager" && strings.TrimSpace(input.ManagerServerID) != "" {
			writeError(w, r, http.StatusBadRequest, "SWARM_MANAGER_MUST_BE_OMITTED")
			return
		}
		task, err := s.runtime.CreateTask(r.Context(), "install_docker_swarm", "server", id, "server", map[string]any{
			"source":          "server-page",
			"mode":            input.Mode,
			"managerServerId": strings.TrimSpace(input.ManagerServerID),
			"advertiseAddr":   input.AdvertiseAddr,
			"connectionMode":  strings.ToLower(strings.TrimSpace(input.ConnectionMode)),
		})
		s.writeOperationRun(w, r, task.ID, err)
	case (strings.HasSuffix(rest, "/install") || strings.HasSuffix(rest, "/uninstall")) && r.Method == http.MethodPost:
		action := serverRuntimeAction(rest)
		if action == "" {
			writeError(w, r, http.StatusNotFound, "SERVER_RUNTIME_ACTION_NOT_FOUND")
			return
		}
		var input emptyServerRuntimeActionRequest
		if !decodeStrict(w, r, &input) {
			return
		}
		if strings.HasSuffix(rest, "/uninstall") {
			component := strings.TrimSuffix(rest, "/uninstall")
			impact, err := s.runtime.ServerRuntimeUninstallImpact(r.Context(), id, component)
			if err != nil && err.Error() != "SWARM_UNINSTALL_BLOCKED_ACTIVE_TASK" {
				writeErrorWithDetails(w, r, http.StatusConflict, err.Error(), map[string]any{"impact": impact})
				return
			}
		}
		task, err := s.runtime.CreateTask(r.Context(), action, "server", id, "server", map[string]any{"source": "server-page"})
		s.writeOperationRun(w, r, task.ID, err)
	default:
		writeError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND")
	}
}

func (s *Server) handleServerRuntimeOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	value, err := s.runtime.ServerRuntimeOverview(r.Context())
	writeResult(w, r, value, err)
}

func (s *Server) idDeleteUpdate(collection string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := lastPathSegment(r.URL.Path)
		switch r.Method {
		case http.MethodPut, http.MethodPatch:
			var input domain.Object
			if !decode(w, r, &input) {
				return
			}
			value, err := s.runtime.UpdateObject(r.Context(), collection, id, input)
			writeResult(w, r, value, err)
		case http.MethodDelete:
			err := s.runtime.DeleteObject(r.Context(), collection, id)
			writeResult(w, r, map[string]any{"ok": true}, err)
		case http.MethodPost:
			task, err := s.runtime.CreateTask(r.Context(), "operate_"+collection, collection, id, collection, map[string]any{})
			writeTask(w, r, task, err)
		default:
			writeError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND")
		}
	}
}

func (s *Server) handleDomainID(w http.ResponseWriter, r *http.Request) {
	id, rest := splitID(strings.TrimPrefix(r.URL.Path, "/secrets/domains/"))
	switch {
	case rest == "":
		s.idDeleteUpdate("domains")(w, r)
	case rest == "redirect" && r.Method == http.MethodPut:
		var input domain.Object
		if !decodeStrict(w, r, &input) {
			return
		}
		value, err := s.runtime.SaveDomainRedirect(r.Context(), id, input)
		writeResult(w, r, value, err)
	case (rest == "redirect/apply" || rest == "redirect/remove") && r.Method == http.MethodPost:
		action := "apply_domain_redirect"
		if rest == "redirect/remove" {
			action = "remove_domain_redirect"
		}
		task, err := s.runtime.CreateDomainRedirectOperation(r.Context(), id, action)
		s.writeOperationRun(w, r, task.ID, err)
	default:
		writeError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND")
	}
}

func (s *Server) handleCertificateID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/secrets/certificates/")
	id, rest := splitID(path)
	if rest == "verify" && r.Method == http.MethodPost {
		task, err := s.runtime.CreateCertificateVerification(r.Context(), id)
		s.writeOperationRun(w, r, task.ID, err)
		return
	}
	if rest == "" && (r.Method == http.MethodPut || r.Method == http.MethodPatch) {
		var input domain.Object
		if !decode(w, r, &input) {
			return
		}
		value, err := s.runtime.UpdateCertificateAsset(r.Context(), id, input)
		writeResult(w, r, value, err)
		return
	}
	s.idDeleteUpdate("certificates")(w, r)
}

func (s *Server) handleResolverID(w http.ResponseWriter, r *http.Request) {
	s.idDeleteUpdate("resolvers")(w, r)
}

func (s *Server) handleIngressID(w http.ResponseWriter, r *http.Request) {
	s.idDeleteUpdate("ingresses")(w, r)
}

func (s *Server) handleRuntimeLogs(w http.ResponseWriter, r *http.Request) {
	targetKind := r.URL.Query().Get("targetKind")
	targetID := firstNonEmpty(r.URL.Query().Get("targetId"), r.URL.Query().Get("applicationId"))
	source := r.URL.Query().Get("source")
	if source == "application" {
		slot, taskID := r.URL.Query().Get("serviceSlotKey"), r.URL.Query().Get("taskId")
		if r.Method != http.MethodPost || targetKind != "application" || targetID == "" || slot == "" {
			writeError(w, r, http.StatusBadRequest, "APPLICATION_LOG_TARGET_INVALID")
			return
		}
		if !terminalOrigin(r) {
			writeError(w, r, http.StatusForbidden, "APPLICATION_LOG_ORIGIN_DENIED")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		value, err := s.runtime.ApplicationCommandLogs(r.Context(), targetID, slot, taskID)
		writeResult(w, r, value, err)
		return
	}
	if source != "" && source != "service" {
		writeError(w, r, http.StatusBadRequest, "APPLICATION_LOG_TARGET_INVALID")
		return
	}
	if source == "service" {
		if r.Method != http.MethodGet || (targetKind != "" && targetKind != "application") {
			writeError(w, r, http.StatusBadRequest, "APPLICATION_LOG_TARGET_INVALID")
			return
		}
		value, err := s.runtime.ApplicationServiceLogs(r.Context(), targetID, r.URL.Query().Get("serviceSlotKey"))
		writeResult(w, r, value, err)
		return
	}
	page, pageSize, ok := readPagination(w, r)
	if !ok {
		return
	}
	order := r.URL.Query().Get("order")
	if order != "" && order != "asc" && order != "desc" {
		writeError(w, r, http.StatusBadRequest, "INVALID_LOG_ORDER")
		return
	}
	value, err := s.runtime.ListRuntimeLogsPage(r.Context(), targetKind, targetID, r.URL.Query().Get("operationRunId"), page, pageSize, order)
	writeResult(w, r, value, err)
}
