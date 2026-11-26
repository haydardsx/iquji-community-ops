package automation

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/application"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type Service struct {
	runtime  *application.Runtime
	grants   *Grants
	bridge   *Bridge
	owner    string
	receipts *receipts
	binary   string
	policyMu sync.Mutex
	requests map[*pendingCall]bool
}

func NewService(rt *application.Runtime, dir, owner, binary string) (*Service, error) {
	grants, err := OpenGrants(dir)
	if err != nil {
		return nil, err
	}
	return &Service{requests: map[*pendingCall]bool{}, runtime: rt, grants: grants, bridge: NewBridge(), owner: owner, binary: binary, receipts: &receipts{dir: filepath.Join(dir, "automation-submissions")}}, nil
}
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /call", s.execute)
	mux.HandleFunc("POST /discovery", func(w http.ResponseWriter, r *http.Request) {
		c, ok := s.grants.Auth(accessToken(r))
		if !ok {
			problem(w, 403, errClientUnauthorized.Error())
			return
		}
		send(w, 200, c.Directory())
	})
	mux.HandleFunc("POST /window/{action}", s.window)
	return mux
}

func send(w http.ResponseWriter, code int, v any) {
	w.Header().Set("content-type", "application/json")
	w.Header().Set("cache-control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, code int, msg string) {
	send(w, code, map[string]string{"errorCode": msg})
}
func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512*1024))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		problem(w, 400, "AUTOMATION_ARGUMENT_INVALID")
		return false
	}
	return true
}
func accessToken(r *http.Request) string {
	value, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return value
}

func (s *Service) window(w http.ResponseWriter, r *http.Request) {
	if s.owner == "" || subtle.ConstantTimeCompare([]byte(accessToken(r)), []byte(s.owner)) != 1 {
		problem(w, 403, "AUTOMATION_WINDOW_REQUIRED")
		return
	}
	switch r.PathValue("action") {
	case "pages":
		send(w, 200, Pages)
	case "clients":
		send(w, 200, map[string]any{"clients": s.grants.List(), "commands": Catalog, "binary": s.binary, "version": Version})
	case "grant", "update":
		var in struct {
			ID          string
			Name        string
			Permissions []Permission
			Rules       []Rule
		}
		if !decode(w, r, &in) {
			return
		}
		if r.PathValue("action") == "update" {
			s.policyMu.Lock()
			c, err := s.grants.Update(in.ID, in.Name, in.Permissions, in.Rules)
			if err == nil {
				s.invalidate(in.ID, &c)
			}
			s.policyMu.Unlock()
			if err != nil {
				if errors.Is(err, errInvalidGrant) {
					problem(w, 400, err.Error())
				} else if err.Error() == "AUTOMATION_CLIENT_NOT_FOUND" {
					problem(w, 404, err.Error())
				} else {
					problem(w, 500, "AUTOMATION_GRANT_FAILED")
				}
				return
			}
			send(w, 200, map[string]any{"client": c})
			return
		}
		c, key, err := s.grants.Add(in.Name, in.Permissions, in.Rules)
		if err != nil {
			if errors.Is(err, errInvalidGrant) {
				problem(w, 400, err.Error())
			} else {
				problem(w, 500, "AUTOMATION_GRANT_FAILED")
			}
			return
		}
		send(w, 201, map[string]any{"client": c, "config": map[string]any{"mcpServers": map[string]any{"iquops": map[string]any{"command": s.binary, "env": map[string]string{"IQUOPS_MCP_TOKEN": key, "IQUOPS_STATE_DIR": s.runtime.Config.StateDir}}}}})
	case "revoke":
		var in struct{ ID string }
		if !decode(w, r, &in) {
			return
		}
		s.policyMu.Lock()
		err := s.grants.Remove(in.ID)
		if err == nil {
			s.invalidate(in.ID, nil)
		}
		s.policyMu.Unlock()
		if err != nil {
			problem(w, 500, "AUTOMATION_REVOKE_FAILED")
			return
		}
		send(w, 200, true)
	case "active", "begin":
		var in struct{ ID string }
		if !decode(w, r, &in) {
			return
		}
		if r.PathValue("action") == "begin" {
			send(w, 200, s.bridge.Begin(in.ID))
		} else {
			send(w, 200, s.bridge.Active(in.ID))
		}
	case "next":
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		send(w, 200, s.bridge.Next(ctx))
	case "reply":
		var in struct {
			ID       string
			Response Response
		}
		if !decode(w, r, &in) {
			return
		}
		send(w, 200, s.bridge.Reply(in.ID, in.Response))
	default:
		problem(w, 404, "AUTOMATION_COMMAND_UNKNOWN")
	}
}
func (s *Service) execute(w http.ResponseWriter, r *http.Request) {
	token := accessToken(r)
	if _, ok := s.grants.Auth(token); !ok {
		problem(w, 403, "AUTOMATION_CLIENT_UNAUTHORIZED")
		return
	}
	var call Call
	if !decode(w, r, &call) {
		return
	}
	cmd, err := call.Validate()
	if err != nil {
		problem(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(s.runtime.WithServerConnections(r.Context()), 100*time.Second)
	defer cancel()
	ctx, client, done, err := s.register(ctx, token, call)
	if err != nil {
		if errors.Is(err, errArgumentRequired) {
			problem(w, 400, err.Error())
			return
		}
		problem(w, 403, err.Error())
		return
	}
	defer done()
	ui := cmd.usesWindow()
	var path string
	if cmd.Name == "form_submit" {
		var existing json.RawMessage
		path, existing, err = s.receipts.reserve(client.ID, call)
		if err != nil {
			problem(w, 409, err.Error())
			return
		}
		if revoked := denied(ctx); revoked != nil {
			if path != "" {
				_ = os.Remove(path)
			}
			problem(w, 403, revoked.Error())
			return
		}
		if existing != nil {
			send(w, 200, existing)
			return
		}
	}
	if ui || cmd.Confirm && !client.Preapproves(call) {
		reply, err := s.bridge.Ask(ctx, client, call, ui, cmd.Confirm && !client.Preapproves(call))
		if err != nil {
			if revoked := denied(ctx); revoked != nil {
				if path != "" && !reply.started {
					_ = os.Remove(path)
				}
				problem(w, 403, revoked.Error())
				return
			}
			// 下列诊断均在业务提交前发生，可以使用同一个键重新提交。
			switch err.Error() {
			case "AUTOMATION_DECLINED", "AUTOMATION_WINDOW_OFFLINE", "AUTOMATION_WINDOW_BUSY", "AUTOMATION_DRAFT_ACTIVE", "AUTOMATION_FORM_INVALID", "AUTOMATION_MANUAL_INPUT_REQUIRED":
				if path != "" {
					_ = os.Remove(path)
				}
			}
			problem(w, 409, err.Error())
			return
		}
		if err := denied(ctx); err != nil {
			problem(w, 403, err.Error())
			return
		}
		if cmd.Confirm && !client.Preapproves(call) && !reply.Approved {
			problem(w, 403, "AUTOMATION_DECLINED")
			return
		}
		if ui {
			if path != "" {
				if err := s.receipts.save(path, receipt{Target: call.Args["target"], Result: reply.Value}); err != nil {
					problem(w, 500, err.Error())
					return
				}
			}
			send(w, 200, reply.Value)
			return
		}
	}
	if err := denied(ctx); err != nil {
		problem(w, 403, err.Error())
		return
	}
	out, err := s.business(ctx, client, call)
	if revoked := denied(ctx); revoked != nil {
		problem(w, 403, revoked.Error())
		return
	}
	if err != nil {
		problem(w, 409, domain.LogSummary(err.Error()))
		return
	}
	send(w, 200, out)
}
func (s *Service) business(ctx context.Context, c Client, call Call) (any, error) {
	target := call.Args["target"]
	switch call.Name {
	case "command_catalog":
		return c.Directory(), nil
	case "monitoring_summary":
		return s.runtime.DashboardSummary(ctx)
	case "projects_list":
		return s.runtime.ListObjects(ctx, "projects")
	case "servers_list":
		return s.runtime.ListObjects(ctx, "servers")
	case "tunnels_list":
		return s.runtime.ListFrpTunnels(ctx)
	case "templates_list":
		return s.runtime.ApplicationTemplates(ctx)
	case "operation_get":
		return s.runtime.GetOperationRun(ctx, target)
	case "application_runtime":
		return s.runtime.ApplicationRuntime(ctx, target)
	case "container_logs":
		return s.runtime.ApplicationServiceLogs(ctx, target, call.Args["slot"])
	case "application_log_configure":
		var argv []string
		if err := json.Unmarshal([]byte(call.Args["argv"]), &argv); err != nil || argv == nil {
			return nil, errors.New("AUTOMATION_ARGUMENT_INVALID")
		}
		return s.runtime.SetApplicationLogCommand(ctx, target, argv)
	case "application_logs":
		return s.runtime.ApplicationCommandLogs(ctx, target, call.Args["slot"], call.Args["task"])
	case "application_release", "application_restart":
		ctx = application.WithTaskRequest(ctx, "mcp-"+c.ID+"-"+tokenHash(call.Args["requestKey"]), tokenHash(call.Name+"\n"+target))
		var task domain.Task
		var err error
		if call.Name == "application_release" {
			task, err = s.runtime.CreateApplicationReleaseOperation(ctx, target, "")
		} else {
			task, err = s.runtime.CreateApplicationOperation(ctx, target, "restart", false)
		}
		if err != nil {
			return nil, err
		}
		if _, err = s.runtime.ConfirmTask(ctx, task.ID); err != nil && err.Error() != "TASK_NOT_CONFIRMABLE" {
			return nil, err
		}
		return s.runtime.GetOperationRun(ctx, task.ID)
	}
	return nil, errors.New("AUTOMATION_COMMAND_UNKNOWN")
}

// 桌面实例仅登记回环端口；保存的客户端令牌与本次启动的窗口凭据独立。
func PublishEndpoint(dir, port string) (func(), error) {
	file, err := os.CreateTemp(dir, ".automation-port-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	_, err = file.WriteString(port)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "automation.port")
	if err = os.Rename(file.Name(), path); err != nil {
		return nil, err
	}
	return func() {
		if current, _ := os.ReadFile(path); string(current) == port {
			_ = os.Remove(path)
		}
	}, nil
}
