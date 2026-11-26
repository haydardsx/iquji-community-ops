package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type dockerRow map[string]any

func (row dockerRow) String(key string) string {
	value, ok := row[key]
	if !ok || value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func (row dockerRow) First(keys ...string) string {
	for _, key := range keys {
		if value := row.String(key); value != "" {
			return value
		}
	}
	return ""
}

func commandAvailable(command string, args ...string) bool {
	_, err := run(command, args...)
	return err == nil
}

func run(command string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func dockerJSONRows(args ...string) []dockerRow {
	output, err := run("docker", args...)
	if err != nil {
		return nil
	}
	return parseDockerJSONRows(output)
}

func parseDockerJSONRows(output string) []dockerRow {
	var rows []dockerRow
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row dockerRow
		if err := json.Unmarshal([]byte(line), &row); err == nil {
			rows = append(rows, row)
		}
	}
	return rows
}
