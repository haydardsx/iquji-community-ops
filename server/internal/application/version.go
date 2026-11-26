package application

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func readVersion(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "config", "app", "app.json"))
	if err != nil {
		return "0.1.1"
	}
	var payload struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || payload.Version == "" {
		return "0.1.1"
	}
	return payload.Version
}
