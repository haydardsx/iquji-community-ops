package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
)

const configPath = "/opt/iquops/monitoring-agent/config.json"

type config struct {
	ServerID        string `json:"serverId"`
	Token           string `json:"token"`
	Version         string `json:"version"`
	ProtocolVersion int    `json:"protocolVersion"`
	Port            int    `json:"port"`
}

func readConfig(path string) (config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config{}, err
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, err
	}
	if cfg.ServerID == "" || cfg.Token == "" {
		return config{}, errors.New("serverId and token are required")
	}
	if cfg.ProtocolVersion == 0 {
		cfg.ProtocolVersion = 1
	}
	if cfg.Port == 0 {
		cfg.Port = 9137
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return config{}, errors.New("监控端口无效")
	}
	return cfg, nil
}

func authorized(cfg config, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "MONITORING_AGENT_ROUTE_NOT_FOUND"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+cfg.Token {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "MONITORING_AGENT_TOKEN_INVALID"})
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		payload = []byte(`{"error":"MONITORING_AGENT_JSON_ENCODE_FAILED"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}
