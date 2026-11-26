package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"time"
)

// Agent 独立发行，按控制面日志合同输出诊断，HTTP 响应继续使用采集协议。
func diagnostic(level, event, message, errorCode string) {
	var id [16]byte
	rand.Read(id[:])
	record := map[string]string{"id": "log_" + hex.EncodeToString(id[:]), "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "level": level, "source": "agent", "event": "agent." + event, "message": message}
	if errorCode != "" {
		record["errorCode"] = errorCode
	}
	_ = json.NewEncoder(os.Stderr).Encode(record)
}
