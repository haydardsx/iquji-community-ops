package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLogQueryUsesOnlyRequestedRunningReplica(t *testing.T) {
	snapshot := ApplicationRuntimeSnapshot{Services: []ApplicationServiceState{{ServiceSlotKey: "web", ServiceID: "service_demo", Tasks: []ApplicationServiceTask{{ID: "replica_demo", Status: "running", NodeID: "node_demo", ServerID: "server_demo"}}}}}
	target, err := applicationLogTarget(snapshot, "app_demo", "web", "replica_demo")
	if err != nil || target.ServiceID != "service_demo" {
		t.Fatal("没有选中指定副本")
	}
	for _, slot := range []string{"other", ""} {
		if _, err := applicationLogTarget(snapshot, "app_demo", slot, "replica_demo"); err == nil {
			t.Fatal("接受了其他服务的副本")
		}
	}
	for _, test := range []struct{ id, diagnostic string }{
		{"replaced", "APPLICATION_LOG_REPLICA_CHANGED"},
		{"", "APPLICATION_LOG_REPLICA_REQUIRED"},
	} {
		if _, err := applicationLogTarget(snapshot, "app_demo", "web", test.id); err == nil || err.Error() != test.diagnostic {
			t.Fatal("运行副本的选择诊断错误", err)
		}
	}
	snapshot.Services[0].Tasks[0].Status = "stopped"
	for _, task := range []string{"", "replica_demo"} {
		if _, err := applicationLogTarget(snapshot, "app_demo", "web", task); err == nil || err.Error() != "APPLICATION_LOG_NO_RUNNING_REPLICA" {
			t.Fatal("未报告服务无运行副本", err)
		}
	}
}

func TestLogCommandKeepsArgumentsAndRedactsOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := strings.Repeat("a", 64)
	remote := &applicationLogTestRemote{fakeApplicationRemote: &fakeApplicationRemote{run: func(command string) (string, bool, error) {
		if strings.HasPrefix(command, "docker ps ") {
			return id, false, nil
		}
		if command == "docker exec "+id+" timeout --help" {
			return "timeout", false, nil
		}
		return "", false, errors.New("unexpected")
	}}, read: func(_ context.Context, command string, limit int) (string, bool, error) {
		if !strings.HasSuffix(command, "'reader' '' 'a b' 'a b'") || !strings.HasPrefix(command, "docker exec "+id+" timeout -s KILL ") || limit != applicationLogMaxBytes {
			t.Fatal("日志查询未按原参数或限制执行")
		}
		return "example-material", false, nil
	}}
	result := ApplicationServiceLogs{}
	err := runApplicationLogCommand(ctx, remote, TerminalTarget{TaskID: "replica_demo", ServiceID: "service_demo", NodeID: "node_demo"}, "main", []string{"reader", "", "a b", "a b"}, []string{"example-material"}, &result)
	if err != nil || len(result.Items) != 1 || result.Items[0].Message == "example-material" || result.Items[0].Source != "application" {
		t.Fatal("应用日志来源或脱敏错误", err)
	}
}
