package application

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"golang.org/x/crypto/ssh"
)

//go:embed monitoring_agent_install.sh
var monitoringAgentInstallScript string

const defaultMonitoringAgentPort = 9137

const monitoringAgentUnitPath = "/etc/systemd/system/iquops-monitoring-agent.service"

func monitoringAgentInstallAction(action string) string {
	return "agent_dir=" + shellQuote(monitoringAgentRemoteDir) + "\nunit=" + shellQuote(monitoringAgentUnitPath) + "\nproc_root=/proc\naction=" + shellQuote(action) + "\n" + monitoringAgentInstallScript
}

func installMonitoringAgentFiles(ctx context.Context, client *ssh.Client, input ServerRuntimeExecutionInput, binary []byte, target string) (domain.Object, error) {
	action := func(ctx context.Context, name string) error {
		return remoteRun(ctx, client, monitoringAgentInstallAction(name))
	}
	if err := remoteRun(ctx, client, monitoringAgentInstallDirectoryCommand()); err != nil {
		return nil, errors.New("MONITORING_AGENT_DIRECTORY_FAILED")
	}
	if err := action(ctx, "recover"); err != nil {
		return nil, errors.New("MONITORING_AGENT_ROLLBACK_FAILED")
	}
	previous, readErr := remoteMonitoringAgentReport(ctx, client)
	serverID := stringValue(input.Server["id"])
	if readErr == nil && stringValue(previous["serverId"]) != "" && stringValue(previous["serverId"]) != serverID {
		return nil, errors.New("MONITORING_AGENT_SERVER_MISMATCH")
	}
	upgrading := readErr == nil && serverID != "" && previous["serverId"] == serverID
	if err := action(ctx, "prepare"); err != nil {
		return nil, errors.New("MONITORING_AGENT_PREPARE_FAILED")
	}
	stage := monitoringAgentRemoteDir + "/.pending-install/"
	input.LogStep("agent", "install", "info", "正在上传监控 Agent "+target)
	if err := writeRemoteFileBytes(ctx, client, stage+"new-binary", binary); err != nil {
		return nil, errors.New("MONITORING_AGENT_UPLOAD_FAILED")
	}
	if !upgrading {
		cfg, err := json.MarshalIndent(map[string]any{"serverId": serverID, "token": domain.NewID("agent_token"), "version": target, "protocolVersion": 1, "port": defaultMonitoringAgentPort}, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := writeRemoteFileBytes(ctx, client, stage+"new-config", cfg); err != nil {
			return nil, errors.New("MONITORING_AGENT_UPLOAD_FAILED")
		}
		if err := writeRemoteFile(ctx, client, stage+"new-unit", monitoringAgentUnit()); err != nil {
			return nil, errors.New("MONITORING_AGENT_UPLOAD_FAILED")
		}
	}
	err := action(ctx, "activate")
	var report domain.Object
	if err == nil {
		report, err = waitForMonitoringAgentReport(ctx, client, serverID)
	} else {
		err = errors.New("MONITORING_AGENT_SERVICE_START_FAILED")
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err != nil {
		recoveryClient := client
		// SSH 会话中断时重新建立控制连接，恢复目录在恢复成功前保留。
		if recoveryErr := action(finishCtx, "recover"); recoveryErr != nil {
			fresh, connectErr := sshClient(finishCtx, input.Server, input.SSHKey)
			if connectErr != nil {
				return nil, errors.New("MONITORING_AGENT_ROLLBACK_FAILED")
			}
			defer fresh.Close()
			if remoteRun(finishCtx, fresh, monitoringAgentInstallAction("recover")) != nil {
				return nil, errors.New("MONITORING_AGENT_ROLLBACK_FAILED")
			}
			recoveryClient = fresh
		}
		if upgrading {
			if _, verifyErr := waitForMonitoringAgentReport(finishCtx, recoveryClient, serverID); verifyErr != nil {
				return nil, errors.New("MONITORING_AGENT_ROLLBACK_FAILED")
			}
		}
		return nil, err
	}
	if err := action(finishCtx, "commit"); err != nil {
		return nil, errors.New("MONITORING_AGENT_COMMIT_FAILED")
	}
	return report, nil
}

func waitForMonitoringAgentReport(ctx context.Context, client sshSessionClient, serverID string) (domain.Object, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	for {
		report, err := remoteMonitoringAgentReport(ctx, client)
		if err == nil {
			if report["serverId"] != serverID {
				return nil, errors.New("MONITORING_AGENT_SERVER_MISMATCH")
			}
			if stringValue(report["sampledAt"]) != "" {
				return report, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, errors.New("MONITORING_AGENT_REPORT_UNAVAILABLE")
		case <-time.After(time.Second):
		}
	}
}
