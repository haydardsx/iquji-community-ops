package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGuideGrantSeparatesDirectTargetsAndDiscovery(t *testing.T) {
	const name = "application_release"
	guide := Permission{Command: name, Targets: []string{}, Mode: ModeGuide}
	direct := Permission{Command: name, Targets: []string{"app-one"}, Mode: ModeDirect}
	if _, err := makeClient("one", "示例", []Permission{guide, direct}, []Rule{{name, "app-two"}}); err == nil {
		t.Fatal("引导范围不能设置免确认规则")
	}
	for _, invalid := range [][]Permission{
		{guide, guide}, {direct, direct}, {{"projects_list", []string{}, ModeGuide}},
		{{GuideCommand, []string{}, ModeDirect}}, {{GuideCommand, []string{}, ModeGuide}},
		{{name, []string{}, ""}}, {{name, []string{}, "other"}},
	} {
		if _, err := makeClient("one", "示例", invalid, nil); err == nil {
			t.Fatal("接受错误的模式或重复权限")
		}
	}
	c, err := makeClient("one", "示例", []Permission{guide, direct}, []Rule{{name, "app-one"}})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Permits(Call{name, map[string]string{"target": "app-one"}}) || c.Permits(Call{name, map[string]string{"target": "app-two"}}) ||
		!c.Permits(Call{GuideCommand, map[string]string{"action": name, "target": "app-two"}}) ||
		c.Permits(Call{GuideCommand, map[string]string{"action": "application_restart", "target": "app-one"}}) ||
		c.Permits(Call{GuideCommand, map[string]string{"action": name, "target": "app-one", "application": "app-two"}}) {
		t.Fatal("原动作或目标范围混用")
	}
	if directory := c.Directory(); len(directory["commands"].([]Command)) != 2 || len(directory["guides"].([]Permission)) != 2 {
		t.Fatal("混合权限发现错误")
	}
	c, err = makeClient("one", "仅引导", []Permission{guide}, nil)
	if err != nil {
		t.Fatal(err)
	}
	commands := c.Directory()["commands"].([]Command)
	if len(commands) != 1 || commands[0].Name != GuideCommand {
		t.Fatal("向仅引导客户端暴露了直接操作")
	}
}

func TestGuideMigrationFreezesV2TargetsAndRejectsExplicitInvalidMode(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "automation-clients.json")
	for _, item := range []string{`{"command":"application_release","targets":["one"],"mode":null}`, `{"command":"application_release","targets":["one"],"mode":""}`, `{"command":"application_release","targets":["one"]}`} {
		old := []byte(`[{"id":"example","name":"示例","hash":"` + tokenHash("example-token") + `","permissions":[` + item + `]}]`)
		if err := os.WriteFile(file, old, 0600); err != nil {
			t.Fatal(err)
		}
		grants, err := OpenGrants(dir)
		if bytes.Contains([]byte(item), []byte(`"mode"`)) {
			if err == nil {
				t.Fatal("无效模式被升级为直操权限")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			c, ok := grants.Auth("example-token")
			if !ok || len(c.Permissions) != 1 || c.Permissions[0].Mode != ModeDirect ||
				!c.Permits(Call{"application_release", map[string]string{"target": "one"}}) ||
				c.Permits(Call{"application_release", map[string]string{"target": "two"}}) ||
				c.Permits(Call{GuideCommand, map[string]string{"action": "application_restart", "target": "one"}}) {
				t.Fatal("迁移扩大了原动作或目标")
			}
			grants, err = OpenGrants(dir)
			if err != nil {
				t.Fatal(err)
			}
		}
		data, _ := os.ReadFile(file)
		if !bytes.Contains(data, []byte(`"mode":"direct"`)) {
			t.Fatal("迁移未持久化")
		}
	}
}

func TestGuideHasNoBusinessSideEffectsAndPendingGuideCanBeWithdrawn(t *testing.T) {
	s := testService(t)
	permissions := []Permission{{"application_release", []string{"app-one"}, ModeGuide}}
	c, token, err := s.grants.Add("引导客户端", permissions, nil)
	if err != nil {
		t.Fatal(err)
	}
	actual := Call{"application_release", map[string]string{"target": "app-one", "requestKey": "retry"}}
	for range 2 {
		if code, _ := request(s, "/call", token, actual); code != 403 {
			t.Fatal("仅引导的真实发布未拒绝", code)
		}
	}
	call := Call{GuideCommand, map[string]string{"action": actual.Name, "target": "app-one"}}
	for _, args := range []map[string]string{
		{"action": actual.Name, "target": "app-two"}, {"action": actual.Name},
		{"action": "application_restart", "target": "app-one"}, {"action": actual.Name, "target": "app-one", "field": "unrelated"},
		{"action": actual.Name, "target": "app-one", "requestKey": "retry"},
		{"action": GuideCommand, "target": "app-one"},
	} {
		code, _ := request(s, "/call", token, Call{GuideCommand, args})
		if code != 403 && code != 400 {
			t.Fatal("非法引导上下文被接受", code)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	s.bridge.lastPoll = time.Now()
	done := make(chan int, 1)
	next := func() *Ticket {
		go func() { code, _ := request(s, "/call", token, call); done <- code }()
		ticket := s.bridge.Next(ctx)
		if ticket == nil || ticket.Confirm || !ticket.Window || ticket.Call.Name != GuideCommand {
			t.Fatal("引导未传递为独立窗口票据")
		}
		return ticket
	}
	ticket := next()
	if !s.bridge.Begin(ticket.ID) || !s.bridge.Reply(ticket.ID, Response{Value: json.RawMessage(`{"shown":true}`)}) || <-done != 200 {
		t.Fatal("窗口未能展示引导")
	}
	state, err := s.runtime.Store.LoadState(ctx)
	if err != nil || len(state.Tasks) != 0 || len(state.ReleasePlans) != 0 {
		t.Fatal("引导产生了真实发布任务", err)
	}
	ticket = next()
	update := map[string]any{"id": c.ID, "name": c.Name, "permissions": []Permission{{"application_release", []string{"app-two"}, ModeGuide}}}
	if code, _ := request(s, "/window/update", s.owner, update); code != 200 || <-done != 403 || s.bridge.Begin(ticket.ID) {
		t.Fatal("已撤回引导目标的旧票据仍可操作")
	}
	if code, _ := request(s, "/call", token, call); code != 403 {
		t.Fatal("原引导重试继续越权")
	}
}

func TestDowngradeCancelsDirectConfirmationAndRetryNeedsNewPermission(t *testing.T) {
	s := testService(t)
	const target = "project.edit/one"
	direct := []Permission{{"form_submit", []string{target}, ModeDirect}}
	guide := []Permission{{"form_submit", []string{target}, ModeGuide}}
	c, token, err := s.grants.Add("窗口示例", direct, nil)
	if err != nil {
		t.Fatal(err)
	}
	update := func(permissions []Permission) {
		t.Helper()
		code, _ := request(s, "/window/update", s.owner, map[string]any{"id": c.ID, "name": c.Name, "permissions": permissions, "rules": []Rule{}})
		if code != 200 {
			t.Fatal("更新权限失败", code)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	s.bridge.lastPoll = time.Now()
	call := Call{"form_submit", map[string]string{"target": target, "requestKey": "retry"}}
	done := make(chan int, 1)
	next := func() *Ticket {
		go func() { code, _ := request(s, "/call", token, call); done <- code }()
		ticket := s.bridge.Next(ctx)
		if ticket == nil || !ticket.Confirm {
			t.Fatal("缺少现场确认票据")
		}
		return ticket
	}
	ticket := next()
	update(guide)
	ack := Response{Approved: true, Value: json.RawMessage(`{"saved":"example"}`)}
	if <-done != 403 || s.bridge.Begin(ticket.ID) || s.bridge.Reply(ticket.ID, ack) {
		t.Fatal("降权后仍允许确认旧操作")
	}
	if code, _ := request(s, "/call", token, call); code != 403 {
		t.Fatal("真实操作在仅引导模式下可重试")
	}
	update(direct)
	ticket = next()
	if !s.bridge.Begin(ticket.ID) || !s.bridge.Reply(ticket.ID, ack) || <-done != 200 {
		t.Fatal("重新直接授权后无法重新确认原请求")
	}
	update(guide)
	if code, raw := request(s, "/call", token, call); code != 403 || bytes.Contains(raw, []byte("saved")) {
		t.Fatal("降权客户端读取了已有收据")
	}
}

func TestGuideRequiresOperationLocatorsBeforeWindowDispatch(t *testing.T) {
	s := testService(t)
	commands := []string{"form_fill", "page_control", "application_logs", "application_release", "application_log_configure", "page_open"}
	permissions := []Permission{}
	for _, command := range commands {
		permissions = append(permissions, Permission{command, []string{}, ModeGuide})
	}
	c, key, err := s.grants.Add("引导定位", permissions, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		action string
		fields map[string]string
		code   int
		reason string
	}{
		{"application_release", nil, 400, "AUTOMATION_ARGUMENT_REQUIRED"},
		{"application_log_configure", map[string]string{"target": " "}, 400, "AUTOMATION_ARGUMENT_REQUIRED"},
		{"form_fill", map[string]string{"target": "project.edit/one"}, 400, "AUTOMATION_ARGUMENT_REQUIRED"},
		{"page_control", map[string]string{"field": "tab"}, 400, "AUTOMATION_ARGUMENT_REQUIRED"},
		{"page_open", map[string]string{"tab": "logs"}, 400, "AUTOMATION_ARGUMENT_REQUIRED"},
		{"application_logs", map[string]string{"target": "app-one", "task": "replica-one"}, 400, "AUTOMATION_ARGUMENT_REQUIRED"},
		{"application_logs", map[string]string{"target": "app-one", "task": "replica-one", "slot": "main"}, 409, "AUTOMATION_WINDOW_OFFLINE"},
		{"application_logs", map[string]string{"target": "app-one"}, 409, "AUTOMATION_WINDOW_OFFLINE"},
		{"application_release", map[string]string{"target": "app-one", "requestKey": "one"}, 400, "AUTOMATION_ARGUMENT_INVALID"},
		{"application_log_configure", map[string]string{"target": "app-one", "argv": "[]"}, 400, "AUTOMATION_ARGUMENT_INVALID"},
		{"form_fill", map[string]string{"target": "project.edit/one", "field": "name", "value": "text"}, 400, "AUTOMATION_ARGUMENT_INVALID"},
		{"page_open", map[string]string{"page": "projects"}, 409, "AUTOMATION_WINDOW_OFFLINE"},
	} {
		args := map[string]string{"action": tc.action}
		for k, v := range tc.fields {
			args[k] = v
		}
		code, out := request(s, "/call", key, Call{GuideCommand, args})
		if code != tc.code || !bytes.Contains(out, []byte(tc.reason)) {
			t.Fatalf("%s 状态码 %d，预期 %s", tc.action, code, tc.reason)
		}
	}
	if _, err := s.grants.Update(c.ID, c.Name, []Permission{{"page_open", []string{"one"}, ModeGuide}}, nil); err != nil {
		t.Fatal(err)
	}
	code, out := request(s, "/call", key, Call{GuideCommand, map[string]string{"action": "page_open", "page": "projects"}})
	if code != 400 || !bytes.Contains(out, []byte("AUTOMATION_ARGUMENT_REQUIRED")) {
		t.Fatal("受限目标的页面引导缺少明确目标仍被接受", code)
	}
}
