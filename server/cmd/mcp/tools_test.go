package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/application"
	"github.com/haydardsx/iquji-community-ops/server/internal/automation"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 子进程运行产品的 stdio 服务，保持标准输出只写 MCP 帧。
func TestStdioHelper(t *testing.T) {
	if os.Getenv("IQUOPS_TEST_STDIO") != "1" {
		return
	}
	if err := serve(context.Background()); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestCLIHelper(t *testing.T) {
	if os.Getenv("IQUOPS_TEST_CLI") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"iquops-mcp"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	os.Exit(1)
}

func TestStdioPermissionsChangeWithoutReconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancel()
	dir := t.TempDir()
	db, err := sqlite.Open(ctx, filepath.Join(dir, "state.sqlite"), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service, err := automation.NewService(application.NewRuntime(platform.Config{StateDir: dir}, db), dir, "example-window", "iquops-mcp")
	if err != nil {
		t.Fatal(err)
	}
	prefix := "/api/automation"
	server := httptest.NewServer(http.StripPrefix(prefix, service.Handler()))
	defer server.Close()
	addr, _ := url.Parse(server.URL)
	cleanup, err := automation.PublishEndpoint(dir, addr.Port())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	window := func(action string, body any, out any) {
		t.Helper()
		b, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+prefix+"/window/"+action, bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer example-window")
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			t.Fatal(action, res.StatusCode)
		}
		if out != nil && json.NewDecoder(res.Body).Decode(out) != nil {
			t.Fatal("授权响应无效")
		}
	}
	var grant struct {
		Client automation.Client
		Config struct {
			MCPServers map[string]struct{ Env map[string]string }
		}
	}
	policy := map[string]any{"name": "示例客户端", "permissions": []automation.Permission{{Command: "command_catalog", Mode: automation.ModeDirect, Targets: []string{}}, {Command: "application_logs", Mode: automation.ModeDirect, Targets: []string{"app-one"}}}, "rules": []automation.Rule{{Command: "application_logs", Target: "app-one"}}}
	window("grant", map[string]any{"name": policy["name"], "permissions": []automation.Permission{}, "rules": []automation.Rule{}}, &grant)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, bin, "-test.run=^TestStdioHelper$")
	command.Env = append(os.Environ(), "IQUOPS_TEST_STDIO=1", "IQUOPS_STATE_DIR="+dir, "IQUOPS_MCP_TOKEN="+grant.Config.MCPServers["iquops"].Env["IQUOPS_MCP_TOKEN"])
	changed := make(chan struct{}, 4)
	client := mcp.NewClient(&mcp.Implementation{Name: "example", Version: "1"}, &mcp.ClientOptions{ToolListChangedHandler: func(_ context.Context, _ *mcp.ToolListChangedRequest) {
		select {
		case changed <- struct{}{}:
		default:
		}
	}})
	waitForChange := func() {
		t.Helper()
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatal("MCP 未通知工具目录变化")
		}
	}
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 0 {
		t.Fatal("空授权的客户端不应发现工具", err)
	}
	// 首次连接可以早于或晚于首次目录刷新。建立空目录基线后，只等待真实授权变化。
	policy["id"] = grant.Client.ID
	window("update", policy, nil)
	waitForChange()
	tools, err = session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 3 {
		t.Fatal("工具发现没有采用客户端白名单", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "application_logs" {
			schema, _ := json.Marshal(tool.InputSchema)
			if !bytes.Contains(schema, []byte(`"enum":["app-one"]`)) || tool.Meta["iquops/confirmationRequired"] != true {
				t.Fatal("缺少授权目标或确认信息")
			}
		}
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "command_catalog", Arguments: map[string]any{}})
	if err != nil || result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "app-one") {
		t.Fatal("运行目录未返回实际客户端范围", err)
	}
	policy["permissions"] = []automation.Permission{{Command: "form_submit", Mode: automation.ModeGuide, Targets: []string{"project.edit/one"}}}
	policy["rules"] = []automation.Rule{}
	window("update", policy, nil)
	waitForChange()
	tools, err = session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != automation.GuideCommand {
		t.Fatal("仅引导客户端仍能发现直接操作", err)
	}
	schema, _ := json.Marshal(tools.Tools[0].InputSchema)
	if !bytes.Contains(schema, []byte(`"enum":["form_submit"]`)) || !bytes.Contains(schema, []byte(`"enum":["project.edit/one"]`)) || tools.Tools[0].Meta["iquops/guideScopes"] == nil {
		t.Fatal("引导发现缺少原动作和目标范围")
	}
	call := func(name string, args map[string]any) {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err == nil && !result.IsError {
			t.Fatal("引导工具接受了未授权操作", name)
		}
	}
	call("form_submit", map[string]any{"target": "project.edit/one", "requestKey": "one"})
	call(automation.GuideCommand, map[string]any{"action": "application_release", "target": "project.edit/one"})
	call(automation.GuideCommand, map[string]any{"action": "form_submit", "target": "project.edit/two"})
	call(automation.GuideCommand, map[string]any{"action": "form_submit", "target": "project.edit/one", "field": "example"})
	cli := func(args ...string) ([]byte, error) {
		child := exec.CommandContext(ctx, bin, append([]string{"-test.run=^TestCLIHelper$", "--"}, args...)...)
		child.Env = append(os.Environ(), "IQUOPS_TEST_CLI=1", "IQUOPS_STATE_DIR="+dir, "IQUOPS_MCP_TOKEN="+grant.Config.MCPServers["iquops"].Env["IQUOPS_MCP_TOKEN"])
		return child.CombinedOutput()
	}
	raw, err := cli("list")
	var discovered struct {
		Commands []automation.Command
		Guides   []automation.Permission
	}
	if err != nil || json.Unmarshal(raw, &discovered) != nil || len(discovered.Commands) != 1 || discovered.Commands[0].Name != automation.GuideCommand || len(discovered.Guides) != 1 {
		t.Fatal("CLI 未从当前客户端权限派生引导目录", err)
	}
	if raw, err := cli("call", "form_submit", `{"target":"project.edit/one","requestKey":"one"}`); err == nil || !bytes.Contains(raw, []byte("AUTOMATION_PERMISSION_DENIED")) {
		t.Fatal("CLI 接受仅引导客户端的真实提交")
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: automation.GuideCommand, Arguments: map[string]any{"action": "form_submit", "target": "project.edit/one"}})
	if err != nil || !result.IsError || result.Content[0].(*mcp.TextContent).Text != "AUTOMATION_WINDOW_OFFLINE" {
		t.Fatal("有效引导未到达独立窗口路径", err)
	}
	policy["permissions"] = []automation.Permission{{Command: "operation_get", Mode: automation.ModeDirect, Targets: []string{"op-two"}}}
	policy["rules"] = []automation.Rule{}
	window("update", policy, nil)
	waitForChange()
	tools, err = session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "operation_get" {
		t.Fatal("长连接的工具列表未实时收窄", err)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "command_catalog", Arguments: map[string]any{}})
	if err == nil && !result.IsError {
		t.Fatal("收回的工具仍能执行")
	}
	window("revoke", map[string]string{"id": grant.Client.ID}, nil)
	if _, err = session.ListTools(ctx, nil); err == nil {
		t.Fatal("已撤销客户端仍能发现工具")
	}
}

func TestGuideSchemaScopesEachActionAndKeepsDirectTargets(t *testing.T) {
	cmd, _ := automation.Lookup(automation.GuideCommand)
	grants := []automation.Permission{
		{Command: "form_fill", Mode: automation.ModeGuide, Targets: []string{"project.edit/one"}},
		{Command: "form_fill", Mode: automation.ModeDirect, Targets: []string{"project.edit/two"}},
		{Command: "application_logs", Mode: automation.ModeDirect, Targets: []string{"app-one"}},
		{Command: "application_logs", Mode: automation.ModeGuide, Targets: []string{}},
	}
	tool := toolGrant{command: cmd, guides: grants}.tool()
	payload, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		OneOf []struct {
			Properties map[string]struct {
				Const string
				Enum  []string
			}
			Required          []string
			DependentRequired map[string][]string
		}
	}
	if err := json.Unmarshal(payload, &schema); err != nil || len(schema.OneOf) != 2 {
		t.Fatal("缺少逐动作范围", err)
	}
	for _, branch := range schema.OneOf {
		action := branch.Properties["action"].Const
		target := branch.Properties["target"]
		switch action {
		case "form_fill":
			if !slices.Equal(target.Enum, []string{"project.edit/one", "project.edit/two"}) || !slices.Contains(branch.Required, "target") {
				t.Fatal("引导没有合并该动作的精确目标")
			}
			if _, ok := branch.Properties["slot"]; ok {
				t.Fatal("混入了另一个动作的参数")
			}
			if _, ok := branch.Properties["value"]; ok {
				t.Fatal("混入了写入参数")
			}
		case "application_logs":
			if target.Enum != nil || !slices.Contains(branch.Required, "target") || slices.Contains(branch.Required, "slot") || !slices.Equal(branch.DependentRequired["task"], []string{"slot"}) {
				t.Fatal("全范围引导意外受到直接权限限制")
			}
		default:
			t.Fatal("未授权动作出现在引导中")
		}
	}
}
