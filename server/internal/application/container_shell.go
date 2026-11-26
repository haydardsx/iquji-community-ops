package application

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func commandShell(raw any) string {
	command, valid := stringArguments(raw)
	if !valid || len(command) < 3 || !strings.HasPrefix(command[1], "-") {
		return ""
	}
	switch path.Base(command[0]) {
	case "sh", "bash", "dash", "ash", "ksh", "zsh":
		return command[0]
	}
	return ""
}

func requiredImageShells(ctx context.Context, node containerPeer, image string, source, swarm domain.Object) ([]string, error) {
	startupConfig := swarm
	if swarm["command"] == nil {
		startupConfig = source
	}
	shell := commandShell(startupConfig["command"])
	if shell != "" && startupConfig["shellEntrypoint"] != true {
		// 镜像有固定入口时，CMD 中即使包含 sh -c 也只是交给该入口的参数。
		raw, err := node.call(ctx, "docker image inspect "+shellQuote(image)+" --format '{{json .Config.Entrypoint}}'", nil, "APPLICATION_IMAGE_SHELL_CHECK_FAILED")
		var entrypoint []string
		if err != nil || json.Unmarshal([]byte(raw), &entrypoint) != nil {
			return nil, errors.New("APPLICATION_IMAGE_SHELL_CHECK_FAILED")
		}
		if len(entrypoint) > 0 && !(len(entrypoint) == 1 && entrypoint[0] == "") {
			shell = ""
		}
	}
	var shells []string
	for _, executable := range []string{shell, commandShell(swarm["initializeCommand"]), commandShell(swarm["healthCommand"])} {
		if executable != "" && !containsString(shells, executable) {
			shells = append(shells, executable)
		}
	}
	return shells, nil
}

// 只运行解释器自己的空操作，目标节点不会在预检中执行应用脚本。
func checkImageShells(ctx context.Context, node containerPeer, image string, source, swarm domain.Object) error {
	shells, err := requiredImageShells(ctx, node, image, source, swarm)
	if err != nil {
		return err
	}
	for _, shell := range shells {
		probe := "docker run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges --entrypoint " + shellQuote(shell) + " " + shellQuote(image) + " -c : >/dev/null 2>&1; code=$?; case $code in 0) ;; 126|127) printf missing ;; *) exit 1 ;; esac"
		result, err := node.call(ctx, probe, nil, "APPLICATION_IMAGE_SHELL_CHECK_FAILED")
		if err != nil {
			return err
		}
		if result == "missing" {
			return errors.New("APPLICATION_IMAGE_SHELL_UNAVAILABLE")
		}
	}
	return nil
}
