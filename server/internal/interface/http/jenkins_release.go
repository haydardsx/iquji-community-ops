package httpapi

import (
	"net/http"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type jenkinsCallbackLog struct {
	Source    string `json:"source"`
	Phase     string `json:"phase"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}
type jenkinsCallbackRequest struct {
	RunID         string               `json:"runId"`
	ReleasePlanID string               `json:"releasePlanId"`
	ApplicationID string               `json:"applicationId"`
	Status        string               `json:"status"`
	Summary       string               `json:"summary"`
	CommitSHA     string               `json:"commitSha,omitempty"`
	ImageDigest   string               `json:"imageDigest,omitempty"`
	JenkinsRun    domain.Object        `json:"jenkinsRun"`
	Artifacts     []domain.Object      `json:"artifacts,omitempty"`
	OutputValues  map[string]string    `json:"outputValues,omitempty"`
	RuntimeLogs   []jenkinsCallbackLog `json:"runtimeLogs,omitempty"`
}

func (s *Server) handleJenkinsRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/platform/jenkins/runs/"), "/")
	if len(parts) != 2 || (parts[1] != "materials" && parts[1] != "git-credential") {
		writeError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND")
		return
	}
	run, allowed, err := s.runtime.AuthorizeJenkinsRun(r.Context(), parts[0], r.Header.Get("x-iquops-jenkins-run-token"))
	if err != nil {
		writeResult(w, r, nil, err)
		return
	}
	if !allowed {
		writeError(w, r, http.StatusUnauthorized, "JENKINS_RUN_UNAUTHORIZED")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if parts[1] == "git-credential" {
		value, err := s.runtime.JenkinsGitCredentialMaterial(r.Context(), run)
		writeResult(w, r, value, err)
		return
	}
	value, err := s.runtime.JenkinsReleaseMaterials(run)
	writeResult(w, r, value, err)
}
func (s *Server) handleJenkinsReleaseArtifacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	var request jenkinsCallbackRequest
	if !decodeStrict(w, r, &request) {
		return
	}
	run, allowed, err := s.runtime.AuthorizeJenkinsCallback(r.Context(), request.RunID, r.Header.Get("x-iquops-jenkins-run-token"))
	if err != nil {
		writeResult(w, r, nil, err)
		return
	}
	if !allowed {
		writeError(w, r, http.StatusUnauthorized, "JENKINS_RUN_UNAUTHORIZED")
		return
	}
	if run["releasePlanId"] != request.ReleasePlanID || run["applicationId"] != request.ApplicationID {
		writeError(w, r, http.StatusConflict, "JENKINS_CALLBACK_RUN_MISMATCH")
		return
	}
	input, err := requestObject(request)
	if err != nil {
		writeResult(w, r, nil, err)
		return
	}
	value, err := s.runtime.RecordJenkinsReleaseCallback(r.Context(), input)
	writeResult(w, r, value, err)
}
func (s *Server) handleBuildLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	value, err := s.runtime.ApplicationBuilds(r.Context(), r.URL.Query().Get("applicationId"))
	writeResult(w, r, value, err)
}
func (s *Server) handleBuildOutput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/logs/builds/"), "/")
	if len(parts) != 2 {
		writeError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND")
		return
	}
	value, err := s.runtime.JenkinsBuildOutput(r.Context(), parts[0], parts[1])
	if err != nil {
		writeResult(w, r, nil, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if content, ok := value["content"].(string); ok {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(content))
		return
	}
	writeJSON(w, http.StatusOK, value["content"])
}
