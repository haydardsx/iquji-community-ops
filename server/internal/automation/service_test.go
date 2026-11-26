package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/application"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

func testService(t *testing.T) *Service {
	t.Helper()
	root := t.TempDir()
	db, err := sqlite.Open(t.Context(), filepath.Join(root, "state.sqlite"), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rt := application.NewRuntime(platform.Config{StateDir: root}, db)
	s, err := NewService(rt, root, randomToken(), "iquops-mcp")
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func request(s *Service, path, key string, value any) (int, []byte) {
	b, _ := json.Marshal(value)
	r := httptest.NewRequest("POST", path, bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}
func TestGrantRevokeAndCatalog(t *testing.T) {
	s := testService(t)
	_, key, err := s.grants.Add("查询客户端", legacyPermissions(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := request(s, "/window/clients", key, nil); code != 403 {
		t.Fatal(code)
	}
	if code, _ := request(s, "/call", key, Call{Name: "projects_list"}); code != 200 {
		t.Fatal(code)
	}
	if code, _ := request(s, "/call", key, Call{Name: "form_submit"}); code != 400 {
		t.Fatal(code)
	}
	if code, _ := request(s, "/call", key, Call{Name: "page_state"}); code != 409 {
		t.Fatal(code)
	}
	code, raw := request(s, "/window/grant", s.owner, map[string]any{"name": "表单客户端", "permissions": legacyPermissions(), "rules": []Rule{{"form_submit", "project.edit/project_example"}}})
	if code != 201 {
		t.Fatal(code)
	}
	var created struct{ Client Client }
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	if !created.Client.Preapproves(Call{"form_submit", map[string]string{"target": "project.edit/project_example"}}) ||
		created.Client.Preapproves(Call{"form_submit", map[string]string{"target": "project.edit/another"}}) {
		t.Fatal("目标授权边界错误")
	}
	if len(Pages) != 5 {
		t.Fatal("页面目录不完整")
	}
	client, _ := s.grants.Auth(key)
	if code, _ = request(s, "/window/revoke", s.owner, map[string]string{"id": client.ID}); code != 200 {
		t.Fatal(code)
	}
	if code, _ = request(s, "/call", key, Call{Name: "projects_list"}); code != 403 {
		t.Fatal(code)
	}
	stored, _ := os.ReadFile(s.grants.path)
	if bytes.Contains(stored, []byte(key)) {
		t.Fatal("授权文件保存了令牌原文")
	}
}

func TestWindowApprovalAndPersistentSubmission(t *testing.T) {
	s := testService(t)
	_, key, err := s.grants.Add("窗口客户端", legacyPermissions(), nil)
	if err != nil {
		t.Fatal(err)
	}
	call := Call{"form_submit", map[string]string{"target": "project.edit/project_example", "requestKey": "repeat"}}
	// 先由窗口发起轮询，再模拟真实窗口的异步响应。
	s.bridge.lastPoll = time.Now()
	done := make(chan int, 1)
	go func() { code, _ := request(s, "/call", key, call); done <- code }()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second*3)
	defer cancel()
	ticket := s.bridge.Next(ctx)
	if ticket == nil || !ticket.Confirm || !s.bridge.Active(ticket.ID) || !s.bridge.Begin(ticket.ID) {
		t.Fatal("缺失窗口确认")
	}
	if !s.bridge.Reply(ticket.ID, Response{Approved: true, Value: json.RawMessage(`{"page":"projects"}`)}) {
		t.Fatal("窗口回传失败")
	}
	if code := <-done; code != 200 {
		t.Fatal(code)
	}
	again, err := NewService(s.runtime, s.runtime.Config.StateDir, s.owner, s.binary)
	if err != nil {
		t.Fatal(err)
	}
	if code, b := request(again, "/call", key, call); code != 200 || !bytes.Contains(b, []byte("projects")) {
		t.Fatal(code)
	}
	call.Args["target"] = "project.edit/another"
	if code, b := request(again, "/call", key, call); code != 409 || !bytes.Contains(b, []byte("REQUEST_KEY_CONFLICT")) {
		t.Fatal(code)
	}
}

// 人工填写秘密前没有发起业务请求，填写后须能沿用原提交键。
func TestManualFieldAllowsRetryWithOriginalKey(t *testing.T) {
	s := testService(t)
	target := "server.jenkins.bind/server_example"
	_, token, err := s.grants.Add("表单客户端", []Permission{{"form_submit", []string{target}, ModeDirect}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	call := Call{"form_submit", map[string]string{"target": target, "requestKey": "manual-input"}}
	s.bridge.lastPoll = time.Now()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for _, result := range []Response{{Approved: true, Error: "AUTOMATION_MANUAL_INPUT_REQUIRED"}, {Approved: true, Value: json.RawMessage(`{"receipt":{"operationRunId":"run_example","status":"queued"}}`)}} {
		done := make(chan int, 1)
		go func() { code, _ := request(s, "/call", token, call); done <- code }()
		ticket := s.bridge.Next(ctx)
		if ticket == nil || !s.bridge.Begin(ticket.ID) || !s.bridge.Reply(ticket.ID, result) {
			t.Fatal("缺少原提交键对应的表单请求")
		}
		want := 200
		if result.Error != "" {
			want = 409
		}
		if code := <-done; code != want {
			t.Fatal(code)
		}
	}
	if code, data := request(s, "/call", token, call); code != 200 || !bytes.Contains(data, []byte("run_example")) {
		t.Fatal("应读回已提交任务")
	}
}

func TestRevokeCancelsPendingAndSeparatesLogPermissions(t *testing.T) {
	s := testService(t)
	client, key, err := s.grants.Add("日志客户端", legacyPermissions(), []Rule{{"application_logs", "app_example"}})
	if err != nil {
		t.Fatal(err)
	}
	call := Call{"application_log_configure", map[string]string{"target": "app_example", "argv": "[]"}}
	if client.Preapproves(call) {
		t.Fatal("读取日志的许可不能修改命令")
	}
	s.bridge.lastPoll = time.Now()
	done := make(chan int, 1)
	go func() { code, _ := request(s, "/call", key, call); done <- code }()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	ticket := s.bridge.Next(ctx)
	if ticket == nil || !ticket.Confirm {
		t.Fatal("需要确认")
	}
	if code, _ := request(s, "/window/revoke", s.owner, map[string]string{"id": client.ID}); code != 200 {
		t.Fatal(code)
	}
	if code := <-done; code == 200 || s.bridge.Active(ticket.ID) {
		t.Fatal("撤销后的请求仍有效")
	}
}
