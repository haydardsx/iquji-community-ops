package platform

import (
	"encoding/json"
	"os"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// Diagnostic 只接收由调用方选择的安全摘要和错误码，标准错误流每行一个完整事件。
func Diagnostic(source, level, event, message, errorCode string) {
	entry := map[string]string{
		"id": domain.NewID("log"), "timestamp": domain.Now(), "source": source,
		"level": level, "event": source + "." + event, "message": domain.LogSummary(message),
	}
	if errorCode != "" {
		entry["errorCode"] = errorCode
	}
	_ = json.NewEncoder(os.Stderr).Encode(entry)
}
