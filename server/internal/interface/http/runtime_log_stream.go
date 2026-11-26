package httpapi

import (
	"net/http"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 订阅读取 SQLite 中的真实记录，同进程与独立 Worker 遵循相同的提交顺序和过滤条件。
func (s *Server) handleRuntimeLogStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
		return
	}
	query := r.URL.Query()
	targetKind := query.Get("targetKind")
	targetID := firstNonEmpty(query.Get("targetId"), query.Get("applicationId"))
	if targetKind == "" && query.Get("operationRunId") == "" {
		targetKind = "application"
	}
	read := func() ([]domain.RuntimeLog, error) {
		return s.runtime.ListRuntimeLogs(r.Context(), targetKind, targetID, query.Get("operationRunId"))
	}
	items, err := read()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "RUNTIME_LOG_READ_FAILED")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	stream := http.NewResponseController(w)
	writeSSE(w, "snapshot", map[string]any{"type": "snapshot", "items": items})
	if stream.Flush() != nil {
		return
	}
	seen := make(map[string]struct{}, len(items))
	for _, entry := range items {
		seen[entry.ID] = struct{}{}
	}
	updates := time.NewTicker(2 * time.Second)
	defer updates.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-updates.C:
			items, err = read()
			if err != nil {
				writeSSE(w, "error", map[string]string{"type": "error", "errorCode": "RUNTIME_LOG_READ_FAILED"})
				_ = stream.Flush()
				return
			}
			next := make(map[string]struct{}, len(items))
			changed := false
			for _, entry := range items {
				next[entry.ID] = struct{}{}
				if _, present := seen[entry.ID]; present {
					continue
				}
				writeSSE(w, "log", map[string]any{"type": "log", "item": entry})
				changed = true
			}
			seen = next
			if changed && stream.Flush() != nil {
				return
			}
		case <-heartbeat.C:
			writeSSE(w, "heartbeat", map[string]string{"type": "heartbeat", "timestamp": domain.Now()})
			if stream.Flush() != nil {
				return
			}
		}
	}
}
