package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/application"
)

func taskIdempotencyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if key == "" || r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		if len(key) > 200 {
			writeError(w, r, http.StatusBadRequest, "IDEMPOTENCY_KEY_INVALID")
			return
		}
		// 只读已受输入大小限制的 JSON 动作；文件上传不进入任务幂等协议。
		if !strings.Contains(r.Header.Get("Content-Type"), "application/json") && r.ContentLength > 0 {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 2*1024*1024+1))
		if err != nil || len(body) > 2*1024*1024 {
			writeError(w, r, http.StatusBadRequest, "REQUEST_BODY_TOO_LARGE")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		hash := sha256.New()
		_, _ = hash.Write([]byte(r.Method + " " + r.URL.RequestURI() + "\n"))
		_, _ = hash.Write(body)
		ctx := application.WithTaskRequest(r.Context(), key, hex.EncodeToString(hash.Sum(nil)))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
