package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/application"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (s *Server) handleOperationLogID(w http.ResponseWriter, r *http.Request) {
	id := lastPathSegment(r.URL.Path)
	value, err := s.runtime.OperationLog(r.Context(), id)
	writeResult(w, r, value, err)
}

func (s *Server) handleMonitoringProjects(w http.ResponseWriter, r *http.Request) {
	value, err := s.runtime.MonitoringProjects(r.Context())
	writeResult(w, r, value, err)
}

func (s *Server) handleMonitoringServers(w http.ResponseWriter, r *http.Request) {
	value, err := s.runtime.MonitoringServers(r.Context())
	writeResult(w, r, value, err)
}

func (s *Server) handleMonitoringSwarm(w http.ResponseWriter, r *http.Request) {
	value, err := s.runtime.MonitoringSwarm(r.Context())
	writeResult(w, r, value, err)
}

func (s *Server) handleMonitoringAgentInstall(w http.ResponseWriter, r *http.Request) {
	var input domain.Object
	if !decode(w, r, &input) {
		return
	}
	serverID := stringValue(input["serverId"])
	if serverID == "" {
		writeError(w, r, http.StatusBadRequest, "SERVER_REQUIRED")
		return
	}
	task, err := s.runtime.CreateTask(r.Context(), "install_monitoring_agent", "server", serverID, "server", map[string]any{"source": "manual-monitoring-agent-install"})
	s.writeOperationRun(w, r, task.ID, err)
}

func (s *Server) handleMonitoringAgentUninstall(w http.ResponseWriter, r *http.Request) {
	var input domain.Object
	if !decode(w, r, &input) {
		return
	}
	serverID := stringValue(input["serverId"])
	if serverID == "" {
		writeError(w, r, http.StatusBadRequest, "SERVER_REQUIRED")
		return
	}
	task, err := s.runtime.CreateTask(r.Context(), "uninstall_monitoring_agent", "server", serverID, "server", map[string]any{"clearLocalData": input["clearLocalData"]})
	s.writeOperationRun(w, r, task.ID, err)
}

func (s *Server) handleMonitoringAgentOrphanCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	value, err := s.runtime.CleanupOrphanMonitoringAgents(r.Context())
	writeResult(w, r, value, err)
}

func (s *Server) handleMonitoringAgentPresence(w http.ResponseWriter, r *http.Request) {
	var input domain.Object
	if !decode(w, r, &input) {
		return
	}
	agent, err := s.runtime.UpsertMonitoringAgent(r.Context(), input, "online")
	writeResult(w, r, map[string]any{"agent": agent}, err)
}

func (s *Server) handleMonitoringAgentReport(w http.ResponseWriter, r *http.Request) {
	var input domain.Object
	if !decode(w, r, &input) {
		return
	}
	value, err := s.runtime.AcceptMonitoringReport(r.Context(), input)
	writeResult(w, r, value, err)
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	defer func() {
		_ = r.Body.Close()
	}()
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_JSON")
		return false
	}
	return true
}

func decodeStrict(w http.ResponseWriter, r *http.Request, target any) bool {
	defer func() {
		_ = r.Body.Close()
	}()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_JSON")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "INVALID_JSON")
		return false
	}
	return true
}

func writeResult(w http.ResponseWriter, r *http.Request, value any, err error) {
	if err != nil {
		message := publicErrorMessage(err.Error())
		status := http.StatusBadRequest
		if errors.Is(err, context.Canceled) {
			message = "OPERATION_CANCELLED"
			status = http.StatusRequestTimeout
		}
		if errors.Is(err, context.DeadlineExceeded) {
			message = "OPERATION_TIMEOUT"
			status = http.StatusRequestTimeout
		}
		if strings.HasSuffix(message, "NOT_FOUND") {
			status = http.StatusNotFound
		}
		if err.Error() == "UNAUTHORIZED" {
			status = http.StatusUnauthorized
		}
		var usageError *application.ApplicationOutputUsageError
		var redirectError *application.DomainRedirectUsageError
		if errors.As(err, &redirectError) {
			writeErrorWithDetails(w, r, http.StatusConflict, err.Error(), map[string]any{"affectedRedirects": redirectError.Rules})
			return
		}
		if errors.As(err, &usageError) {
			writeErrorWithDetails(w, r, http.StatusConflict, usageError.Code, map[string]any{"affectedApplications": usageError.Applications})
			return
		}
		var activeTaskError *application.TaskAlreadyActiveError
		if errors.As(err, &activeTaskError) {
			writeErrorWithDetails(w, r, http.StatusConflict, err.Error(), map[string]any{"activeTaskId": activeTaskError.ActiveTaskID})
			return
		}
		var serverDeleteError *application.ServerDeleteBlockedError
		if errors.As(err, &serverDeleteError) {
			writeErrorWithDetails(w, r, http.StatusConflict, err.Error(), map[string]any{"impact": serverDeleteError.Impact})
			return
		}
		var releasePreviewError *application.ReleasePlanPreviewBlockedError
		if errors.As(err, &releasePreviewError) {
			writeErrorWithDetails(w, r, http.StatusBadRequest, err.Error(), map[string]any{
				"blockers": releasePreviewError.Blockers,
				"checks":   releasePreviewError.Checks,
			})
			return
		}
		if err.Error() == "TASK_NOT_CONFIRMABLE" || err.Error() == "TASK_NOT_CANCELLABLE" || err.Error() == "IDEMPOTENCY_KEY_CONFLICT" {
			status = http.StatusConflict
		}
		writeError(w, r, status, message)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func writeTask(w http.ResponseWriter, r *http.Request, task domain.Task, err error) {
	if err != nil {
		writeResult(w, r, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, domain.TaskResponse(task))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, message string) {
	writeErrorWithDetails(w, r, status, message, nil)
}

func writeErrorWithDetails(w http.ResponseWriter, r *http.Request, status int, message string, details map[string]any) {
	message = publicErrorMessage(message)
	traceID := r.Header.Get("x-trace-id")
	if traceID == "" {
		traceID = domain.NewID("trace")
	}
	payload := map[string]any{"errorCode": message, "message": message, "traceId": traceID}
	if details != nil {
		payload["details"] = details
	}
	writeJSON(w, status, payload)
}

func publicErrorMessage(message string) string {
	code, detail, _ := strings.Cut(message, ":")
	if !domain.IsPublicDiagnosticCode(code) {
		return "OPERATION_FAILED"
	}
	switch code {
	case "FRP_TUNNEL_INVALID", "FRP_TUNNEL_PREVIEW_BLOCKED", "RELEASE_PLAN_PREVIEW_BLOCKED":
		var blockers []string
		for _, item := range strings.Split(detail, ",") {
			blocker, _, _ := strings.Cut(strings.TrimSpace(item), ":")
			if domain.IsPublicDiagnosticCode(blocker) {
				blockers = append(blockers, blocker)
			}
		}
		if len(blockers) > 0 {
			return code + ":" + strings.Join(blockers, ",")
		}
	}
	return code
}

func writeSSE(w http.ResponseWriter, event string, payload any) {
	data, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "event: %s\n", event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", string(data))
}

func traceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("x-trace-id")
		if traceID == "" {
			traceID = domain.NewID("trace")
			r.Header.Set("x-trace-id", traceID)
		}
		w.Header().Set("x-trace-id", traceID)
		next.ServeHTTP(w, r)
	})
}

func splitID(value string) (string, string) {
	parts := strings.SplitN(strings.Trim(value, "/"), "/", 2)
	if len(parts) == 0 {
		return "", ""
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

func lastPathSegment(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func objectStringSliceHTTP(value any) []string {
	switch items := value.(type) {
	case []string:
		return items
	case []any:
		result := make([]string, 0, len(items))
		for _, item := range items {
			if text := stringValue(item); text != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		return []string{}
	}
}

func objectSliceHTTP(value any) []domain.Object {
	switch items := value.(type) {
	case []domain.Object:
		return items
	case []any:
		result := make([]domain.Object, 0, len(items))
		for _, item := range items {
			switch typed := item.(type) {
			case domain.Object:
				result = append(result, typed)
			case map[string]any:
				result = append(result, domain.Object(typed))
			}
		}
		return result
	default:
		return []domain.Object{}
	}
}

func objectMap(value any) domain.Object {
	switch typed := value.(type) {
	case domain.Object:
		return cloneMap(typed)
	case map[string]any:
		return cloneMap(domain.Object(typed))
	default:
		return domain.Object{}
	}
}

func cloneMap(input domain.Object) domain.Object {
	next := domain.Object{}
	for key, value := range input {
		next[key] = value
	}
	return next
}

func serverRuntimeAction(rest string) string {
	switch rest {
	case "docker/install":
		return string(domain.TaskActionInstallDockerEngine)
	case "docker/uninstall":
		return string(domain.TaskActionUninstallDockerEngine)
	case "swarm/install":
		return string(domain.TaskActionInstallDockerSwarm)
	case "swarm/uninstall":
		return string(domain.TaskActionUninstallDockerSwarm)
	case "traefik/install":
		return string(domain.TaskActionInstallTraefikGateway)
	case "traefik/uninstall":
		return string(domain.TaskActionUninstallTraefikGateway)
	case "frps/install":
		return string(domain.TaskActionInstallFrps)
	case "frps/uninstall":
		return string(domain.TaskActionUninstallFrps)
	case "jenkins/install":
		return string(domain.TaskActionInstallJenkins)
	case "jenkins/uninstall":
		return string(domain.TaskActionUninstallJenkins)
	default:
		return ""
	}
}
