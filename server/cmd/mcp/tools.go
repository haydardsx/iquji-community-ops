package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/automation"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type toolGrant struct {
	command  automation.Command
	targets  []string
	approved []string
	guides   []automation.Permission
}

func (g toolGrant) tool() *mcp.Tool {
	props := map[string]any{}
	for _, key := range g.command.Fields {
		props[key] = map[string]any{"type": "string"}
	}
	required := slices.Clone(g.command.Required)
	if len(g.targets) > 0 {
		props["target"] = map[string]any{"type": "string", "enum": g.targets}
		if !slices.Contains(required, "target") {
			required = append(required, "target")
		}
	}
	schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	meta := mcp.Meta{"iquops/group": g.command.Group, "iquops/confirmationRequired": g.command.Confirm, "iquops/preapprovedTargets": g.approved}
	if g.command.Name == automation.GuideCommand {
		addGuideScopes(schema, props, g.guides)
		meta["iquops/guideScopes"] = g.guides
	}
	return &mcp.Tool{Name: g.command.Name, Description: g.command.Description, InputSchema: schema, Meta: meta}
}

type toolsRegistry struct {
	mu     sync.Mutex
	server *mcp.Server
	grants map[string]toolGrant
}

func (registry *toolsRegistry) refresh(ctx context.Context) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, err := requestDesktop(ctx, "discovery", nil)
	var directory struct {
		Version     int
		Commands    []automation.Command
		Guides      []automation.Permission
		Permissions []automation.Permission
		Rules       []automation.Rule
	}
	if err == nil {
		if json.Unmarshal(raw, &directory) != nil || directory.Permissions == nil || directory.Commands == nil {
			err = errors.New("AUTOMATION_RESPONSE_INVALID")
		} else if directory.Version != automation.Version {
			err = errors.New("AUTOMATION_VERSION_MISMATCH")
		}
	}
	next := map[string]toolGrant{}
	if err == nil {
		for _, cmd := range directory.Commands {
			if cmd.Name == automation.GuideCommand {
				if len(directory.Guides) > 0 {
					next[cmd.Name] = toolGrant{command: cmd, guides: directory.Guides}
				}
				continue
			}
			var targets []string
			for _, permission := range directory.Permissions {
				if permission.Command == cmd.Name && permission.Mode == automation.ModeDirect {
					targets = permission.Targets
					break
				}
			}
			if targets == nil {
				continue
			}
			approved := []string{}
			for _, r := range directory.Rules {
				if r.Command == cmd.Name {
					approved = append(approved, r.Target)
				}
			}
			next[cmd.Name] = toolGrant{command: cmd, targets: targets, approved: approved}
		}
	}
	for name := range registry.grants {
		if _, ok := next[name]; !ok {
			registry.server.RemoveTools(name)
		}
	}
	for name, g := range next {
		if old, ok := registry.grants[name]; ok && reflect.DeepEqual(old, g) {
			continue
		}
		registry.server.AddTool(g.tool(), func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			call := automation.Call{Name: name}
			if len(req.Params.Arguments) > 0 && json.Unmarshal(req.Params.Arguments, &call.Args) != nil {
				return nil, errors.New("AUTOMATION_ARGUMENT_INVALID")
			}
			raw, err := invoke(ctx, call)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil
		})
	}
	registry.grants = next
	return err
}

func serve(ctx context.Context) error {
	server := mcp.NewServer(&mcp.Implementation{Name: "iquops", Version: strconv.Itoa(automation.Version)}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}},
	})
	registry := &toolsRegistry{server: server}
	// 每次发现或执行都获取当前权限；窗口修改授权时也向已连接的 MCP 客户端发送目录更新。
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/list" || method == "tools/call" {
				if err := registry.refresh(ctx); err != nil {
					return nil, err
				}
			}
			return next(ctx, method, req)
		}
	})
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		tick := time.NewTicker(3 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				_ = registry.refresh(ctx)
			}
		}
	}()
	return server.Run(ctx, &mcp.StdioTransport{})
}
