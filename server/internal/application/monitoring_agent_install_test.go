package application

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestMonitoringAgentActivationAndRollbackUseAtomicFiles(t *testing.T) {
	for _, update := range []bool{true, false} {
		label := "重新安装"
		if update {
			label = "原位更新"
		}
		t.Run(label, func(t *testing.T) {
			root := t.TempDir()
			write := func(name, data string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0700); err != nil {
					t.Fatal(err)
				}
			}
			read := func(name string) string {
				t.Helper()
				b, err := os.ReadFile(filepath.Join(root, name))
				if err != nil {
					t.Fatal(err)
				}
				return string(b)
			}
			write("iquops-monitoring-agent", "old")
			write("config.json", "old-config")
			write("unit", "old-unit")
			write("systemctl", "#!/bin/sh\necho \"$1\" >> \"$agent_dir/operations\"\ncase $1 in show) echo MainPID=42;; esac\n")
			if err := os.Mkdir(filepath.Join(root, "42"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root, "iquops-monitoring-agent"), filepath.Join(root, "42", "exe")); err != nil {
				t.Fatal(err)
			}
			action := func(name string) {
				t.Helper()
				cmd := exec.Command("/bin/sh", "-c", monitoringAgentInstallScript)
				cmd.Env = []string{"PATH=" + root + ":" + os.Getenv("PATH"), "agent_dir=" + root, "unit=" + filepath.Join(root, "unit"), "proc_root=" + root, "action=" + name}
				if err := cmd.Run(); err != nil {
					t.Fatalf("安装代理时 %s 阶段失败", name)
				}
			}
			action("prepare")
			previous, err := os.Open(filepath.Join(root, "iquops-monitoring-agent"))
			if err != nil {
				t.Fatal(err)
			}
			defer previous.Close()
			write(".pending-install/new-binary", "new")
			if !update {
				write(".pending-install/new-config", "new-config")
				write(".pending-install/new-unit", "new-unit")
			}
			action("activate")
			old := make([]byte, 3)
			if _, err := previous.Read(old); err != nil || string(old) != "old" || read("iquops-monitoring-agent") != "new" {
				t.Fatal("新上传改写了旧进程的文件")
			}
			if update && (read("config.json") != "old-config" || read("unit") != "old-unit" || strings.Contains(read("operations"), "daemon-reload")) {
				t.Fatal("在线更新改写了配置或服务定义")
			}
			if !update && (read("config.json") != "new-config" || read("unit") != "new-unit") {
				t.Fatal("重新安装未应用新配置")
			}
			action("recover")
			if read("iquops-monitoring-agent") != "old" || read("config.json") != "old-config" || read("unit") != "old-unit" || strings.Count(read("operations"), "restart") != 2 {
				t.Fatal("回滚未恢复并重新启动旧版本")
			}
		})
	}
}

func TestMonitoringAgentReportValidatesServerAndPersistence(t *testing.T) {
	client, _, _ := runtimeProbeSSH(t, func(command string) (string, uint32) {
		if !strings.Contains(command, "Authorization: Bearer $token") || !strings.Contains(command, "/report") {
			return "", 1
		}
		return `{"serverId":"different","sampledAt":"2026-01-01T00:00:00Z"}`, 0
	})
	if _, err := waitForMonitoringAgentReport(context.Background(), client, "target"); err == nil || err.Error() != "MONITORING_AGENT_SERVER_MISMATCH" {
		t.Fatal("接受了其他服务器的报告", err)
	}
	r, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	if err := r.applyMonitoringAgentInstallResult(ctx, "srv_gateway", domain.Object{"agent": domain.Object{"status": "online"}}); err == nil || err.Error() != "MONITORING_AGENT_REPORT_INVALID" {
		t.Fatal("缺少真实报告仍登记为安装成功", err)
	}
}
