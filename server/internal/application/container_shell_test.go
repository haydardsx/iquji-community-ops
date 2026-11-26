package application

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestShellProbeFollowsContainerExecutionSemantics(t *testing.T) {
	script := []string{"/bin/sh", "-c", "exit 0"}
	for _, test := range []struct {
		name, entrypoint string
		source, swarm    domain.Object
		inspect, probe   bool
	}{
		{"固定入口接收字面参数", `["/app"]`, nil, domain.Object{"command": script}, true, false},
		{"软件包固定入口接收字面参数", `["/app"]`, domain.Object{"command": script}, nil, true, false},
		{"服务参数覆盖软件包脚本", `["/app"]`, domain.Object{"command": script}, domain.Object{"command": []string{"serve"}}, false, false},
		{"空入口执行命令", `null`, nil, domain.Object{"command": script}, true, true},
		{"显式清空镜像入口执行命令", `[""]`, nil, domain.Object{"command": script}, true, true},
		{"显式覆盖服务入口", `["/app"]`, nil, domain.Object{"command": script, "shellEntrypoint": true}, false, true},
		{"软件包脚本清除镜像入口", `[]`, domain.Object{"command": script, "shellEntrypoint": true}, nil, false, true},
		{"固定入口仍核验健康命令", `["/app"]`, nil, domain.Object{"healthCommand": script}, false, true},
		{"固定入口仍核验初始化命令", `["/app"]`, nil, domain.Object{"initializeCommand": script}, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var inspected, probed atomic.Bool
			client, _, _ := runtimeProbeSSH(t, func(cmd string) (string, uint32) {
				switch {
				case strings.HasPrefix(cmd, "docker image inspect "):
					inspected.Store(true)
					return test.entrypoint, 0
				case strings.HasPrefix(cmd, "docker run --rm --network none "):
					probed.Store(true)
					return "missing", 0
				}
				return "", 1
			})
			err := checkImageShells(context.Background(), containerPeer{client: client}, "example:1", test.source, test.swarm)
			if inspected.Load() != test.inspect || probed.Load() != test.probe || (test.probe && (err == nil || err.Error() != "APPLICATION_IMAGE_SHELL_UNAVAILABLE")) || (!test.probe && err != nil) {
				t.Fatalf("不符合镜像运行语义：inspect=%v, probe=%v, err=%v", inspected.Load(), probed.Load(), err)
			}
		})
	}
}
