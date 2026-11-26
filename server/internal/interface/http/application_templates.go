package httpapi

import (
	"net/http"

	"github.com/haydardsx/iquji-community-ops/server/internal/application"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (s *Server) handleApplicationTemplates(w http.ResponseWriter, r *http.Request, parts []string) {
	ctx := r.Context()
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	switch {
	case len(parts) == 0 && r.Method == http.MethodGet:
		doc, err := s.runtime.ApplicationTemplates(ctx)
		writeResult(w, r, doc, err)
	case len(parts) == 2 && parts[0] == "import" && (parts[1] == "preview" || parts[1] == "apply") && r.Method == http.MethodPost:
		var input application.TemplateImport
		if decodeStrict(w, r, &input) {
			value, err := s.runtime.ImportApplicationTemplates(ctx, input, parts[1] == "preview")
			writeResult(w, r, value, err)
		}
	case len(parts) == 0 && r.Method == http.MethodPost, len(parts) == 1 && r.Method == http.MethodPut:
		var item domain.Object
		if decodeStrict(w, r, &item) {
			id := domain.StringValue(item["id"])
			if len(parts) == 1 {
				id = parts[0]
			}
			err := s.runtime.SaveApplicationTemplate(ctx, id, item, len(parts) == 0)
			writeResult(w, r, item, err)
		}
	case len(parts) == 1 && r.Method == http.MethodDelete:
		err := s.runtime.DeleteApplicationTemplate(ctx, parts[0])
		writeResult(w, r, domain.Object{"ok": true}, err)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
	}
}
