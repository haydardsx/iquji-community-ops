package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestExplicitPermissionPolicyAndDiscovery(t *testing.T) {
	s := testService(t)
	for _, p := range []any{nil, []Permission{{"projects_list", []string{"project-one"}, ModeDirect}}, []Permission{{"form_open", nil, ModeDirect}}, []Permission{{"missing", []string{}, ModeDirect}}} {
		if code, _ := request(s, "/window/grant", s.owner, map[string]any{"name": "示例", "permissions": p}); code != 400 {
			t.Fatal("接受了无效权限", code)
		}
	}
	permissions := []Permission{{"command_catalog", []string{}, ModeDirect}, {"form_submit", []string{"project.edit/one", "project.edit/two"}, ModeDirect}}
	if _, _, err := s.grants.Add("示例", permissions, []Rule{{"form_submit", "project.edit/three"}}); err == nil {
		t.Fatal("越过目标范围设置预授权")
	}
	client, token, err := s.grants.Add("示例", permissions, []Rule{{"form_submit", "project.edit/one"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/call", "/discovery"} {
		code, raw := request(s, path, token, Call{Name: "command_catalog"})
		if code != 200 {
			t.Fatal(code)
		}
		var directory struct {
			Commands    []Command
			Permissions []Permission
			Rules       []Rule
		}
		if err := json.Unmarshal(raw, &directory); err != nil || len(directory.Commands) != 3 || len(directory.Permissions) != 2 || len(directory.Rules) != 1 {
			t.Fatal("授权目录与白名单不符", err)
		}
	}
	for _, call := range []Call{{Name: "projects_list"}, {"form_submit", map[string]string{"target": "project.edit/three", "requestKey": "one"}}} {
		if code, _ := request(s, "/call", token, call); code != 403 {
			t.Fatal("允许了越权读取或目标", code)
		}
	}
	if code, _ := request(s, "/window/update", s.owner, map[string]any{"id": client.ID, "name": "停用", "permissions": []Permission{}, "rules": []Rule{}}); code != 200 {
		t.Fatal(code)
	}
	if code, _ := request(s, "/call", token, Call{Name: "command_catalog"}); code != 403 {
		t.Fatal("空权限仍可执行命令")
	}
	code, raw := request(s, "/discovery", token, nil)
	if code != 200 || !bytes.Contains(raw, []byte(`"commands":[]`)) {
		t.Fatal("空权限未给出空工具列表")
	}
}

func TestLegacyMigrationFreezesCommandNames(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "automation-clients.json")
	data, _ := json.Marshal([]map[string]any{{"id": "old", "name": "示例", "hash": tokenHash("old-token"), "rules": []Rule{{"form_submit", "project.edit/one"}}}, {"id": "empty", "name": "空授权", "hash": tokenHash("empty-token"), "permissions": []Permission{}}})
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	g, err := OpenGrants(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := g.Auth("old-token")
	if !ok || len(c.Permissions) != 20 || !c.Preapproves(Call{"form_submit", map[string]string{"target": "project.edit/one"}}) || c.Permits(Call{Name: "future_action"}) {
		t.Fatal("旧发行授权迁移失败")
	}
	empty, _ := g.Auth("empty-token")
	if empty.Permits(Call{Name: "projects_list"}) {
		t.Fatal("扩张了显式空权限")
	}
	saved, err := os.ReadFile(file)
	if err != nil || !bytes.Contains(saved, []byte(`"permissions":[{"command":`)) {
		t.Fatal("迁移未持久化")
	}
}

func TestNarrowingInvalidatesTicketsAndReleasesUnstartedReceipt(t *testing.T) {
	s := testService(t)
	target := "project.edit/one"
	entries := []Permission{{"form_submit", []string{target}, ModeDirect}}
	client, token, err := s.grants.Add("示例", entries, []Rule{{"form_submit", target}})
	if err != nil {
		t.Fatal(err)
	}
	s.bridge.lastPoll = time.Now()
	call := Call{"form_submit", map[string]string{"target": target, "requestKey": "one"}}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	next := func() (*Ticket, chan int) {
		done := make(chan int, 1)
		go func() { code, _ := request(s, "/call", token, call); done <- code }()
		ticket := s.bridge.Next(ctx)
		if ticket == nil {
			t.Fatal("没有收到操作票据")
		}
		return ticket, done
	}
	ticket, done := next()
	if ticket.Confirm {
		t.Fatal("精确预授权仍需确认")
	}
	// 仅清除预授权，保留同一目标的基本访问权。
	code, _ := request(s, "/window/update", s.owner, map[string]any{"id": client.ID, "name": client.Name, "permissions": entries, "rules": []Rule{}})
	if code != 200 || <-done != 403 || s.bridge.Active(ticket.ID) || s.bridge.Begin(ticket.ID) {
		t.Fatal("收窄后旧免确认票据仍有效")
	}
	ticket, done = next()
	if !ticket.Confirm || !s.bridge.Begin(ticket.ID) || s.bridge.Begin(ticket.ID) {
		t.Fatal("重试未进入一次性现场确认")
	}
	if !s.bridge.Reply(ticket.ID, Response{Approved: true, Value: json.RawMessage(`{}`)}) || <-done != 200 {
		t.Fatal("重新确认后提交失败")
	}
	code, _ = request(s, "/window/update", s.owner, map[string]any{"id": client.ID, "name": client.Name, "permissions": []Permission{{"form_submit", []string{target}, ModeGuide}}, "rules": []Rule{}})
	if code != 200 {
		t.Fatal(code)
	}
	if code, _ := request(s, "/call", token, call); code != 403 {
		t.Fatal("权限收回后仍回传历史收据")
	}
}

func TestPermissionTargetAndPreapprovalLimits(t *testing.T) {
	const command = "application_logs"
	targets := make([]string, 100)
	rules := make([]Rule, 100)
	for i := range targets {
		targets[i] = "app-" + strconv.Itoa(i)
		rules[i] = Rule{command, targets[i]}
	}
	if _, err := makeClient("one", "示例", []Permission{{command, targets, ModeDirect}}, rules); err != nil {
		t.Fatal(err)
	}
	if _, err := makeClient("one", "示例", []Permission{{command, append(targets, "app-extra"), ModeDirect}}, nil); err == nil {
		t.Fatal("接受了过多目标")
	}
	if _, err := makeClient("one", "示例", []Permission{{command, []string{}, ModeDirect}}, append(rules, Rule{command, "app-extra"})); err == nil {
		t.Fatal("接受了过多免确认规则")
	}
	for _, value := range []string{"app-*", "*", strings.Repeat("示", 54), "含\n换行", " one"} {
		if _, err := makeClient("one", "示例", []Permission{{command, []string{value}, ModeDirect}}, nil); err == nil {
			t.Fatal("接受了不合法的权限目标")
		}
		if _, err := makeClient("one", "示例", []Permission{{command, []string{}, ModeDirect}}, []Rule{{command, value}}); err == nil {
			t.Fatal("接受了不合法的预授权目标")
		}
	}
	maximum := strings.Repeat("示", 53) + "x"
	if _, err := makeClient("one", "示例", []Permission{{command, []string{maximum}, ModeDirect}}, []Rule{{command, maximum}}); err != nil {
		t.Fatal("拒绝了恰好 160 字节的目标", err)
	}
}

func TestTutorialPermissionsAreScopedToOneGuide(t *testing.T) {
	const target = "project-create"
	var permissions []Permission
	for _, command := range Catalog {
		if strings.HasPrefix(command.Name, "tutorial_") {
			permissions = append(permissions, Permission{command.Name, []string{target}, ModeDirect})
		}
	}
	c, err := makeClient("guide", "示例指引", permissions, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range permissions {
		if !c.Permits(Call{p.Command, map[string]string{"target": target}}) ||
			c.Permits(Call{p.Command, map[string]string{"target": "workspace"}}) ||
			c.Permits(Call{Name: p.Command}) {
			t.Fatalf("教程动作 %s 未正确限制目标", p.Command)
		}
	}
	if c.Permits(Call{"form_submit", map[string]string{"target": "project.create"}}) {
		t.Fatal("仅获教程权限却可以提交项目表单")
	}
}
