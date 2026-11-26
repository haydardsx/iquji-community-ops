package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
)

func main() {
	cfg, err := readConfig(configPath)
	if err != nil {
		diagnostic("error", "config_read_failed", "监控 Agent 配置读取失败", "MONITORING_AGENT_CONFIG_INVALID")
		os.Exit(1)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", authorized(cfg, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":              true,
			"serverId":        cfg.ServerID,
			"version":         cfg.Version,
			"protocolVersion": cfg.ProtocolVersion,
			"lastError":       "",
		})
	}))
	mux.HandleFunc("/report", authorized(cfg, func(w http.ResponseWriter, _ *http.Request) {
		result := collect(cfg)
		writeJSON(w, http.StatusOK, result)
	}))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "MONITORING_AGENT_ROUTE_NOT_FOUND"})
	})

	address := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		diagnostic("error", "listen_failed", "监控 Agent 无法监听", "MONITORING_AGENT_LISTEN_FAILED")
		os.Exit(1)
	}
	diagnostic("info", "listening", "监控 Agent 已开始监听", "")
	if err := http.Serve(listener, mux); err != nil {
		diagnostic("error", "serve_failed", "监控 Agent 服务异常退出", "MONITORING_AGENT_SERVE_FAILED")
		os.Exit(1)
	}
}
