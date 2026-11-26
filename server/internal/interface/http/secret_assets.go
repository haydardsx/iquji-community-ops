package httpapi

import (
	"net/http"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// handleSecretAssets 是凭据资产的唯一集合出口；创建后返回轻量 OperationRun。
func (s *Server) handleSecretAssets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		value, err := s.runtime.ListObjects(r.Context(), "secretAssets")
		writeResult(w, r, value, err)
	case http.MethodPost:
		var input secretAssetCreateRequest
		if !decodeStrict(w, r, &input) {
			return
		}
		payload, err := requestObject(input)
		if err != nil {
			writeResult(w, r, nil, err)
			return
		}
		s.writeSecretAssetOperation(w, r, "create_secret_asset", payload, "")
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
	}
}

// handleSecretAssetID 提供受限更新、材料轮换和带引用检查的删除。
func (s *Server) handleSecretAssetID(w http.ResponseWriter, r *http.Request) {
	id, rest := splitID(strings.TrimPrefix(r.URL.Path, "/secrets/assets/"))
	if id == "" {
		writeError(w, r, http.StatusNotFound, "SECRET_ASSET_NOT_FOUND")
		return
	}
	switch {
	case rest == "" && r.Method == http.MethodDelete:
		if err := s.runtime.DeleteSecretAsset(r.Context(), id); err != nil {
			writeResult(w, r, nil, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case rest == "" && (r.Method == http.MethodPut || r.Method == http.MethodPatch):
		var input secretAssetUpdateRequest
		if !decodeStrict(w, r, &input) {
			return
		}
		payload, err := requestObject(input)
		if err != nil {
			writeResult(w, r, nil, err)
			return
		}
		asset, err := s.runtime.UpdateSecretAsset(r.Context(), id, payload)
		writeResult(w, r, asset, err)
	case rest == "secret" && r.Method == http.MethodPut:
		var input secretAssetRotateRequest
		if !decodeStrict(w, r, &input) {
			return
		}
		s.writeSecretAssetOperation(w, r, "rotate_secret_asset", domain.Object{"secretValue": input.SecretValue}, id)
	default:
		writeError(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND")
	}
}

// writeSecretAssetOperation 暂存材料并创建待确认任务，正式材料只由 Worker 生效。
func (s *Server) writeSecretAssetOperation(w http.ResponseWriter, r *http.Request, action string, payload domain.Object, assetID string) {
	asset, task, err := s.runtime.PrepareSecretAssetOperation(r.Context(), action, payload, assetID)
	if err != nil {
		writeResult(w, r, nil, err)
		return
	}

	operation, err := s.runtime.GetOperationRun(r.Context(), task.ID)
	if err != nil {
		writeResult(w, r, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"asset": asset, "operation": operation})
}

func firstNonEmptyHTTP(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
