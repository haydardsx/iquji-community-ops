package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/application"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type Server struct {
	runtime   *application.Runtime
	mux       *http.ServeMux
	terminals terminalTickets
}

type serverSwarmInstallRequest struct {
	Mode            string `json:"mode"`
	ManagerServerID string `json:"managerServerId,omitempty"`
	AdvertiseAddr   string `json:"advertiseAddr,omitempty"`
	ConnectionMode  string `json:"connectionMode,omitempty"`
}

func New(runtime *application.Runtime) *Server {
	server := &Server{runtime: runtime, mux: http.NewServeMux()}
	server.routes()
	return server
}

func (s *Server) Handler() http.Handler {
	handler := traceMiddleware(taskIdempotencyMiddleware(s.mux))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(s.runtime.WithServerConnections(r.Context())))
	})
}

func (s *Server) routes() {
	s.mux.HandleFunc("POST /servers/terminal-tickets", s.handleTerminalTicket)
	s.mux.HandleFunc("GET /servers/terminal-stream", s.handleTerminalStream)
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/platform/local-config", s.handleLocalConfig)
	s.mux.HandleFunc("/platform/jenkins/runs/", s.handleJenkinsRun)
	s.mux.HandleFunc("/platform/jenkins/release-artifacts", s.handleJenkinsReleaseArtifacts)
	s.mux.HandleFunc("/operations/dashboard-summary", s.handleDashboardSummary)
	s.mux.HandleFunc("/operations/runs", s.handleOperationRuns)
	s.mux.HandleFunc("/operations/activity", s.handleOperationActivity)
	s.mux.HandleFunc("/operations/runs/", s.handleOperationRunID)
	s.mux.HandleFunc("/operations/delete-preview", s.handleDeletePreview)
	s.mux.HandleFunc("/operations/data-maintenance/clear-logs/preview", s.handleDataMaintenancePreview(domain.DataMaintenanceClearLogs))
	s.mux.HandleFunc("/operations/data-maintenance/clear-logs/execute", s.handleDataMaintenanceExecute(domain.DataMaintenanceClearLogs))
	s.mux.HandleFunc("/operations/data-maintenance/factory-reset/preview", s.handleDataMaintenancePreview(domain.DataMaintenanceFactoryReset))
	s.mux.HandleFunc("/operations/data-maintenance/factory-reset/execute", s.handleDataMaintenanceExecute(domain.DataMaintenanceFactoryReset))
	s.mux.HandleFunc("/backup-restore/backups/preview", s.handleBackupPreview)
	s.mux.HandleFunc("/backup-restore/backups/download", s.handleBackupDownload)
	s.mux.HandleFunc("/backup-restore/restores/uploads", s.handleRestoreUpload)
	s.mux.HandleFunc("/backup-restore/restores/preview", s.handleRestorePreview)
	s.mux.HandleFunc("/backup-restore/restores/execute", s.handleRestoreExecute)
	s.mux.HandleFunc("/logs/builds", s.handleBuildLogs)
	s.mux.HandleFunc("/logs/builds/", s.handleBuildOutput)
	s.mux.HandleFunc("/logs/runtime/stream", s.handleRuntimeLogStream)
	s.mux.HandleFunc("/logs/runtime", s.handleRuntimeLogs)
	s.mux.HandleFunc("/logs/operations", s.handleOperationLogs)
	s.mux.HandleFunc("/projects", s.handleProjects)
	s.mux.HandleFunc("/projects/", s.handleProjectsNested)
	s.mux.HandleFunc("/servers", s.handleServers)
	s.mux.HandleFunc("/servers/runtime-overview", s.handleServerRuntimeOverview)
	s.mux.HandleFunc("/servers/tunnels", s.handleFrpTunnels)
	s.mux.HandleFunc("/servers/tunnels/", s.handleFrpTunnelID)
	s.mux.HandleFunc("/servers/", s.handleServersNested)
	s.mux.HandleFunc("/secrets/assets", s.handleSecretAssets)
	s.mux.HandleFunc("/secrets/assets/", s.handleSecretAssetID)
	s.mux.HandleFunc("/secrets/domains", s.handleDomains)
	s.mux.HandleFunc("/secrets/domains/", s.handleDomainID)
	s.mux.HandleFunc("/secrets/certificates", s.handleCertificates)
	s.mux.HandleFunc("/secrets/certificates/", s.handleCertificateID)
	s.mux.HandleFunc("/secrets/resolvers", s.handleResolvers)
	s.mux.HandleFunc("/secrets/resolvers/", s.handleResolverID)
	s.mux.HandleFunc("/secrets/ingresses", s.handleIngresses)
	s.mux.HandleFunc("/secrets/ingresses/", s.handleIngressID)
	s.mux.HandleFunc("/monitoring/agents", s.list("monitoringAgents"))
	s.mux.HandleFunc("/monitoring/agents/install", s.handleMonitoringAgentInstall)
	s.mux.HandleFunc("/monitoring/agents/uninstall", s.handleMonitoringAgentUninstall)
	s.mux.HandleFunc("/monitoring/agents/orphan-cleanup", s.handleMonitoringAgentOrphanCleanup)
	s.mux.HandleFunc("/monitoring/projects", s.handleMonitoringProjects)
	s.mux.HandleFunc("/monitoring/servers", s.handleMonitoringServers)
	s.mux.HandleFunc("/monitoring/swarm", s.handleMonitoringSwarm)
	s.mux.HandleFunc("/monitoring/agents/register", s.handleMonitoringAgentPresence)
	s.mux.HandleFunc("/monitoring/agents/heartbeat", s.handleMonitoringAgentPresence)
	s.mux.HandleFunc("/monitoring/agents/report", s.handleMonitoringAgentReport)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLocalConfig(w http.ResponseWriter, r *http.Request) {
	value, err := s.runtime.LocalConfig(r.Context())
	writeResult(w, r, value, err)
}

func (s *Server) handleDashboardSummary(w http.ResponseWriter, r *http.Request) {
	value, err := s.runtime.DashboardSummary(r.Context())
	writeResult(w, r, value, err)
}

func (s *Server) handleDataMaintenancePreview(action domain.DataMaintenanceAction) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
			return
		}
		value, err := s.runtime.PreviewDataMaintenance(r.Context(), action)
		writeResult(w, r, value, err)
	}
}

func (s *Server) handleDataMaintenanceExecute(action domain.DataMaintenanceAction) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
			return
		}
		var input domain.DataMaintenanceExecuteRequest
		if !decodeStrict(w, r, &input) {
			return
		}
		value, err := s.runtime.ExecuteDataMaintenance(r.Context(), action, input)
		writeResult(w, r, value, err)
	}
}

func (s *Server) handleBackupPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	var input domain.BackupPreviewRequest
	if !decodeStrict(w, r, &input) {
		return
	}
	value, err := s.runtime.PreviewBackup(r.Context(), input)
	writeResult(w, r, value, err)
}

func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	var input domain.BackupDownloadRequest
	if !decodeStrict(w, r, &input) {
		return
	}
	archive, filename, _, err := s.runtime.CreateBackupArchive(r.Context(), input)
	if err != nil {
		writeResult(w, r, nil, err)
		return
	}
	path := archive.Name()
	defer func() {
		_ = archive.Close()
		_ = os.Remove(path)
	}()
	info, err := archive.Stat()
	if err != nil {
		writeResult(w, r, nil, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, archive)
}

func (s *Server) handleRestoreUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	const maximumRestoreUploadBytes = int64(2 << 30)
	r.Body = http.MaxBytesReader(w, r.Body, maximumRestoreUploadBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "RESTORE_MULTIPART_REQUIRED")
		return
	}
	stagingDir := filepath.Join(s.runtime.Config.StateDir, "restore-staging")
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		writeResult(w, r, nil, err)
		return
	}
	upload, err := os.CreateTemp(stagingDir, ".incoming-*.iquops-backup")
	if err != nil {
		writeResult(w, r, nil, err)
		return
	}
	uploadPath := upload.Name()
	defer func() {
		_ = upload.Close()
		_ = os.Remove(uploadPath)
	}()
	if err := upload.Chmod(0o600); err != nil {
		writeResult(w, r, nil, err)
		return
	}
	passphrase := ""
	fileSeen := false
	passphraseSeen := false
	for {
		part, partErr := reader.NextPart()
		if errors.Is(partErr, io.EOF) {
			break
		}
		if partErr != nil {
			writeError(w, r, http.StatusBadRequest, "RESTORE_MULTIPART_INVALID")
			return
		}
		switch part.FormName() {
		case "file":
			if fileSeen || part.FileName() == "" {
				_ = part.Close()
				writeError(w, r, http.StatusBadRequest, "RESTORE_FILE_INVALID")
				return
			}
			fileSeen = true
			if _, err := io.Copy(upload, part); err != nil {
				_ = part.Close()
				var maximumBytesError *http.MaxBytesError
				if errors.As(err, &maximumBytesError) {
					writeError(w, r, http.StatusRequestEntityTooLarge, "RESTORE_FILE_TOO_LARGE")
					return
				}
				writeResult(w, r, nil, err)
				return
			}
		case "passphrase":
			if passphraseSeen {
				_ = part.Close()
				writeError(w, r, http.StatusBadRequest, "RESTORE_PASSPHRASE_INVALID")
				return
			}
			passphraseSeen = true
			value, err := io.ReadAll(io.LimitReader(part, 4097))
			if err != nil || len(value) > 4096 {
				_ = part.Close()
				writeError(w, r, http.StatusBadRequest, "RESTORE_PASSPHRASE_INVALID")
				return
			}
			passphrase = string(value)
		default:
			_ = part.Close()
			writeError(w, r, http.StatusBadRequest, "RESTORE_MULTIPART_FIELD_UNKNOWN")
			return
		}
		_ = part.Close()
	}
	if !fileSeen {
		writeError(w, r, http.StatusBadRequest, "RESTORE_FILE_REQUIRED")
		return
	}
	if err := upload.Sync(); err != nil {
		writeResult(w, r, nil, err)
		return
	}
	if _, err := upload.Seek(0, io.SeekStart); err != nil {
		writeResult(w, r, nil, err)
		return
	}
	value, err := s.runtime.StageRestoreUpload(r.Context(), upload, passphrase)
	writeResult(w, r, value, err)
}

func (s *Server) handleRestorePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	var input domain.RestorePreviewRequest
	if !decodeStrict(w, r, &input) {
		return
	}
	value, err := s.runtime.PreviewRestore(r.Context(), input)
	writeResult(w, r, value, err)
}

func (s *Server) handleRestoreExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	var input domain.RestoreExecuteRequest
	if !decodeStrict(w, r, &input) {
		return
	}
	task, err := s.runtime.CreateRestoreTask(r.Context(), input)
	writeTask(w, r, task, err)
}

func (s *Server) handleDeletePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	value, err := s.runtime.DeletePreview(r.Context(), r.URL.Query().Get("targetKind"), r.URL.Query().Get("targetId"))
	writeResult(w, r, value, err)
}

func (s *Server) list(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
			return
		}
		value, err := s.runtime.ListObjects(r.Context(), name)
		writeResult(w, r, value, err)
	}
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	s.collection(w, r, "projects")
}

func (s *Server) handleServers(w http.ResponseWriter, r *http.Request) {
	s.collection(w, r, "servers")
}

func (s *Server) handleFrpTunnels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		value, err := s.runtime.ListFrpTunnels(r.Context())
		writeResult(w, r, value, err)
	case http.MethodPost:
		var input domain.FrpTunnelInput
		if !decodeStrict(w, r, &input) {
			return
		}
		value, err := s.runtime.CreateFrpTunnel(r.Context(), input)
		writeResult(w, r, value, err)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
	}
}

func (s *Server) handleFrpTunnelID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/servers/tunnels/")
	if path == "probe/status" {
		s.handleFrpTunnelProbeStatus(w, r)
		return
	}
	if path == "preview" && r.Method == http.MethodPost {
		var input domain.FrpTunnelInput
		if !decodeStrict(w, r, &input) {
			return
		}
		value, err := s.runtime.PreviewFrpTunnelFor(r.Context(), strings.TrimSpace(r.URL.Query().Get("tunnelId")), input)
		writeResult(w, r, value, err)
		return
	}
	id, rest := splitID(path)
	switch {
	case rest == "" && r.Method == http.MethodGet:
		value, err := s.runtime.GetFrpTunnel(r.Context(), id)
		writeResult(w, r, value, err)
	case rest == "" && r.Method == http.MethodPut:
		var input domain.FrpTunnelInput
		if !decodeStrict(w, r, &input) {
			return
		}
		value, err := s.runtime.UpdateFrpTunnel(r.Context(), id, input)
		writeResult(w, r, value, err)
	case rest == "" && r.Method == http.MethodDelete:
		task, err := s.runtime.DeleteFrpTunnel(r.Context(), id)
		writeTask(w, r, task, err)
	case rest == "start" && r.Method == http.MethodPost:
		value, err := s.runtime.StartFrpTunnel(r.Context(), id)
		writeResult(w, r, value, err)
	case rest == "stop" && r.Method == http.MethodPost:
		task, err := s.runtime.StopFrpTunnel(r.Context(), id)
		writeTask(w, r, task, err)
	case rest == "runs" && r.Method == http.MethodGet:
		page, pageSize, ok := readPagination(w, r)
		if !ok {
			return
		}
		value, err := s.runtime.ListFrpTunnelRuns(r.Context(), id, page, pageSize)
		writeResult(w, r, value, err)
	case rest == "probe-hits" && r.Method == http.MethodGet:
		page, pageSize, ok := readPagination(w, r)
		if !ok {
			return
		}
		value, err := s.runtime.FrpProbeHits(r.Context(), id, page, pageSize)
		writeResult(w, r, value, err)
	case rest == "probe-hits" && r.Method == http.MethodDelete:
		value, err := s.runtime.ClearFrpProbeHits(r.Context(), id)
		writeResult(w, r, value, err)
	default:
		writeError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND")
	}
}

func (s *Server) handleFrpTunnelProbeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		value, err := s.runtime.FrpProbeStatus(r.Context())
		writeResult(w, r, value, err)
		return
	}
	writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
}

func readPagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	page := 1
	pageSize := 20
	for key, target := range map[string]*int{"page": &page, "pageSize": &pageSize} {
		raw := strings.TrimSpace(r.URL.Query().Get(key))
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || (key == "pageSize" && value > 100) {
			writeError(w, r, http.StatusBadRequest, "INVALID_PAGINATION")
			return 0, 0, false
		}
		*target = value
	}
	return page, pageSize, true
}
