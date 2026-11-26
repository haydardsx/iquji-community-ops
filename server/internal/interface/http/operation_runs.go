package httpapi

import (
	"net/http"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/application"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (s *Server) handleOperationRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	page, pageSize, ok := readPagination(w, r)
	if !ok {
		return
	}
	section, ok := readOperationSection(w, r)
	if !ok {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && !domain.IsOperationRunStatus(status) {
		writeError(w, r, http.StatusBadRequest, "INVALID_OPERATION_RUN_STATUS")
		return
	}
	value, err := s.runtime.ListOperationRuns(r.Context(), application.TaskQuery{
		Action: r.URL.Query().Get("action"), Status: status,
		Target: r.URL.Query().Get("target"), TargetKind: r.URL.Query().Get("targetKind"),
		TargetID: r.URL.Query().Get("targetId"), Page: page, PageSize: pageSize,
		ProjectID: r.URL.Query().Get("projectId"), ApplicationID: r.URL.Query().Get("applicationId"),
		ServerID: r.URL.Query().Get("serverId"),
		TunnelID: r.URL.Query().Get("tunnelId"), Category: r.URL.Query().Get("category"),
		Navigation: r.URL.Query().Get("navigation"),
		Section:    section,
	})
	writeResult(w, r, value, err)
}

func (s *Server) handleOperationRunID(w http.ResponseWriter, r *http.Request) {
	id, rest := splitID(strings.TrimPrefix(r.URL.Path, "/operations/runs/"))
	if id == "" {
		writeError(w, r, http.StatusNotFound, "OPERATION_RUN_NOT_FOUND")
		return
	}
	switch {
	case rest == "" && r.Method == http.MethodGet:
		value, err := s.runtime.GetOperationRun(r.Context(), id)
		writeResult(w, r, value, err)
	case rest == "confirm" && r.Method == http.MethodPost:
		task, err := s.runtime.ConfirmTask(r.Context(), id)
		s.writeOperationRun(w, r, task.ID, err)
	case rest == "cancel" && r.Method == http.MethodPost:
		task, err := s.runtime.CancelTask(r.Context(), id)
		s.writeOperationRun(w, r, task.ID, err)
	case rest == "read" && r.Method == http.MethodPost:
		var input struct {
			Status    string `json:"status"`
			UpdatedAt string `json:"updatedAt"`
		}
		if !decode(w, r, &input) {
			return
		}
		if err := s.runtime.MarkOperationRead(r.Context(), id, input.Status, input.UpdatedAt); err != nil {
			if err.Error() == "OPERATION_READ_VERSION_CHANGED" {
				writeError(w, r, http.StatusConflict, err.Error())
				return
			}
			writeResult(w, r, nil, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND")
	}
}

func (s *Server) handleOperationActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	section, ok := readOperationSection(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	value, err := s.runtime.OperationActivity(r.Context(), application.TaskQuery{
		Section: section, TargetKind: query.Get("targetKind"), TargetID: query.Get("targetId"),
		ProjectID: query.Get("projectId"), ApplicationID: query.Get("applicationId"),
		ServerID: query.Get("serverId"),
		TunnelID: query.Get("tunnelId"), Category: query.Get("category"), Navigation: query.Get("navigation"),
	})
	writeResult(w, r, value, err)
}

func (s *Server) handleOperationLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	page, pageSize, ok := readPagination(w, r)
	if !ok {
		return
	}
	section, ok := readOperationSection(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	value, err := s.runtime.ListOperationLogPage(r.Context(), application.TaskQuery{
		Action: query.Get("action"), Status: query.Get("status"),
		TargetKind: query.Get("targetKind"), TargetID: query.Get("targetId"),
		ProjectID: query.Get("projectId"), ApplicationID: query.Get("applicationId"),
		ServerID: query.Get("serverId"),
		TunnelID: query.Get("tunnelId"), Category: query.Get("category"),
		Navigation: query.Get("navigation"), Page: page, PageSize: pageSize,
		Section: section,
	})
	writeResult(w, r, value, err)
}

func readOperationSection(w http.ResponseWriter, r *http.Request) (string, bool) {
	section := strings.TrimSpace(r.URL.Query().Get("section"))
	if !application.IsOperationSection(section) {
		writeError(w, r, http.StatusBadRequest, "INVALID_OPERATION_SECTION")
		return "", false
	}
	return section, true
}

func (s *Server) writeOperationRun(w http.ResponseWriter, r *http.Request, id string, err error) {
	if err != nil {
		writeResult(w, r, nil, err)
		return
	}
	value, err := s.runtime.GetOperationRun(r.Context(), id)
	writeResult(w, r, value, err)
}

func (s *Server) writeInstallationOperation(w http.ResponseWriter, r *http.Request, installation domain.Object, task domain.Task, err error) {
	if err != nil {
		writeResult(w, r, nil, err)
		return
	}
	operation, err := s.runtime.GetOperationRun(r.Context(), task.ID)
	writeResult(w, r, map[string]any{"installation": installation, "operation": operation}, err)
}
