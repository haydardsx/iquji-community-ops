package httpapi

import (
	"net/http"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/application"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (s *Server) handleDomains(w http.ResponseWriter, r *http.Request) {
	s.collection(w, r, "domains")
}

func (s *Server) handleCertificates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		value, err := s.runtime.ListObjects(r.Context(), "certificates")
		writeResult(w, r, value, err)
	case http.MethodPost:
		var input domain.Object
		if !decode(w, r, &input) {
			return
		}
		value, err := s.runtime.CreateCertificate(r.Context(), input)
		writeResult(w, r, value, err)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
	}
}

func (s *Server) handleResolvers(w http.ResponseWriter, r *http.Request) {
	s.collection(w, r, "resolvers")
}

func (s *Server) handleIngresses(w http.ResponseWriter, r *http.Request) {
	s.collection(w, r, "ingresses")
}

func (s *Server) collection(w http.ResponseWriter, r *http.Request, collection string) {
	s.collectionNamed(w, r, collection, collection)
}

func (s *Server) collectionNamed(w http.ResponseWriter, r *http.Request, listName string, collection string) {
	switch r.Method {
	case http.MethodGet:
		value, err := s.runtime.ListObjects(r.Context(), listName)
		writeResult(w, r, value, err)
	case http.MethodPost:
		var input domain.Object
		if !decode(w, r, &input) {
			return
		}
		value, err := s.runtime.CreateObject(r.Context(), collection, application.SlugPrefix(collection), input)
		writeResult(w, r, value, err)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
	}
}

func (s *Server) handleProjectsNested(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/projects/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND")
		return
	}
	if parts[0] == "application-templates" {
		s.handleApplicationTemplates(w, r, parts[1:])
		return
	}
	if parts[0] == "applications" {
		s.handleApplicationRoutes(w, r, parts[1:])
		return
	}
	if parts[0] == "release-plans" && len(parts) >= 2 {
		s.handleReleasePlanRoutes(w, r, parts[1:])
		return
	}
	projectID := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodPut:
			var input domain.Object
			if !decode(w, r, &input) {
				return
			}
			value, err := s.runtime.UpdateObject(r.Context(), "projects", projectID, input)
			writeResult(w, r, value, err)
		case http.MethodDelete:
			err := s.runtime.DeleteObject(r.Context(), "projects", projectID)
			writeResult(w, r, map[string]any{"ok": true}, err)
		default:
			writeError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND")
		}
		return
	}
	switch parts[1] {
	case "workspace":
		value, err := s.runtime.Workspace(r.Context(), projectID, r.URL.Query().Get("applicationId"))
		writeResult(w, r, value, err)
	case "applications":
		if r.Method == http.MethodGet {
			value, err := s.runtime.ProjectApplications(r.Context(), projectID)
			writeResult(w, r, value, err)
			return
		}
		if r.Method == http.MethodPost {
			var input domain.Object
			if !decode(w, r, &input) {
				return
			}
			input["projectId"] = projectID
			value, err := s.runtime.CreateObject(r.Context(), "applications", "app", input)
			writeResult(w, r, value, err)
			return
		}
		fallthrough
	default:
		writeError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND")
	}
}

func (s *Server) handleApplicationRoutes(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 {
		writeError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND")
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			value, err := s.runtime.ApplicationDetail(r.Context(), id)
			writeResult(w, r, value, err)
		case http.MethodPut:
			var input domain.Object
			if !decode(w, r, &input) {
				return
			}
			value, err := s.runtime.UpdateObject(r.Context(), "applications", id, input)
			writeResult(w, r, value, err)
		case http.MethodDelete:
			err := s.runtime.DeleteObject(r.Context(), "applications", id)
			writeResult(w, r, map[string]any{"ok": true}, err)
		default:
			writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		}
		return
	}
	leaf := strings.Join(parts[1:], "/")
	if leaf == "rebuild" && r.Method == http.MethodPost {
		var request struct{}
		if r.ContentLength != 0 && !decodeStrict(w, r, &request) {
			return
		}
		task, err := s.runtime.CreateApplicationReleaseOperation(r.Context(), id, "")
		s.writeOperationRun(w, r, task.ID, err)
		return
	}
	switch {
	case leaf == "deployment-eligibility" && r.Method == http.MethodGet:
		value, err := s.runtime.ApplicationDeploymentEligibility(r.Context(), id, strings.TrimSpace(r.URL.Query().Get("deploymentManagerServerId")))
		writeResult(w, r, value, err)
	case leaf == "runtime" && r.Method == http.MethodGet:
		value, err := s.runtime.ApplicationRuntime(r.Context(), id)
		writeResult(w, r, value, err)
	case leaf == "deployment-config" && r.Method == http.MethodPut:
		var input domain.Object
		if !decodeStrict(w, r, &input) {
			return
		}
		value, err := s.runtime.UpsertApplicationConfig(r.Context(), id, input)
		writeResult(w, r, value, err)
	case leaf == "release-plans/preview" && r.Method == http.MethodPost:
		var request releasePlanRequest
		if !decodeStrict(w, r, &request) {
			return
		}
		input, err := requestObject(request)
		if err != nil {
			writeResult(w, r, nil, err)
			return
		}
		value, err := s.runtime.ReleasePlanPreview(r.Context(), id, input)
		writeResult(w, r, value, err)
	case leaf == "release-plans" && r.Method == http.MethodPost:
		var request releasePlanRequest
		if !decodeStrict(w, r, &request) {
			return
		}
		input, err := requestObject(request)
		if err != nil {
			writeResult(w, r, nil, err)
			return
		}
		value, err := s.runtime.CreateReleasePlan(r.Context(), id, input)
		writeResult(w, r, value, err)
	case strings.HasPrefix(leaf, "routes"):
		s.handleApplicationRouteCollection(w, r, id, parts[1:])
	case isApplicationAction(leaf) && r.Method == http.MethodPost:
		if leaf == "uninstall/preview" {
			value, err := s.runtime.ApplicationRuntime(r.Context(), id)
			writeResult(w, r, map[string]any{"uninstallPreview": value, "removeVolumes": false}, err)
			return
		}
		var request struct {
			RemoveVolumes bool `json:"removeVolumes"`
		}
		if r.ContentLength != 0 {
			if !decodeStrict(w, r, &request) {
				return
			}
		}
		task, err := s.runtime.CreateApplicationOperation(r.Context(), id, leaf, request.RemoveVolumes)
		s.writeOperationRun(w, r, task.ID, err)
	default:
		writeError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND")
	}
}
