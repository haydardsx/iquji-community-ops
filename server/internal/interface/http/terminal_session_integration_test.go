package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"golang.org/x/crypto/ssh"
)

type terminalNodeFixture struct {
	listener    net.Listener
	privateKey  string
	fingerprint string
	started     atomic.Int32
	stopped     atomic.Int32
	query       func(string) (string, bool)
}

func newTerminalNode(t *testing.T, query func(string) (string, bool)) *terminalNodeFixture {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.MarshalPrivateKey(private, "test")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	n := &terminalNodeFixture{listener: listener, privateKey: string(pem.EncodeToMemory(key)), fingerprint: ssh.FingerprintSHA256(signer.PublicKey()), query: query}
	config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, public ssh.PublicKey) (*ssh.Permissions, error) {
		if !bytes.Equal(public.Marshal(), signer.PublicKey().Marshal()) {
			return nil, fmt.Errorf("测试密钥不匹配")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	go func() {
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			go n.serve(raw, config)
		}
	}()
	return n
}

func (n *terminalNodeFixture) serve(raw net.Conn, config *ssh.ServerConfig) {
	defer raw.Close()
	conn, channels, requests, err := ssh.NewServerConn(raw, config)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(requests)
	for ch := range channels {
		if ch.ChannelType() != "session" {
			_ = ch.Reject(ssh.UnknownChannelType, "session")
			continue
		}
		stream, reqs, err := ch.Accept()
		if err != nil {
			return
		}
		go n.session(stream, reqs)
	}
}

func (n *terminalNodeFixture) session(stream ssh.Channel, requests <-chan *ssh.Request) {
	defer stream.Close()
	var pty struct {
		Term                      string
		Cols, Rows, Width, Height uint32
		Modes                     string
	}
	var started bool
	for req := range requests {
		switch req.Type {
		case "pty-req":
			if err := ssh.Unmarshal(req.Payload, &pty); err != nil {
				return
			}
			_ = req.Reply(pty.Term == "xterm-256color", nil)
		case "window-change":
			var size struct{ Cols, Rows, Width, Height uint32 }
			if err := ssh.Unmarshal(req.Payload, &size); err != nil {
				return
			}
			_, _ = fmt.Fprintf(stream, "resize %dx%d", size.Cols, size.Rows)
		case "shell", "exec":
			var input struct{ Command string }
			if req.Type == "exec" && ssh.Unmarshal(req.Payload, &input) != nil {
				return
			}
			_ = req.Reply(true, nil)
			if input.Command != "" && !strings.HasPrefix(input.Command, "exec docker exec -it ") {
				if strings.HasPrefix(input.Command, "cat >/dev/null; exec docker exec ") {
					_, _ = io.Copy(io.Discard, stream)
				}
				value, ok := n.query(input.Command)
				code := uint32(1)
				if ok {
					code = 0
				}
				_, _ = io.WriteString(stream, value)
				_, _ = stream.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{code}))
				return
			}
			if started || pty.Cols == 0 {
				return
			}
			started = true
			n.started.Add(1)
			go func(command string) {
				defer n.stopped.Add(1)
				defer stream.Close()
				scanner := bufio.NewScanner(stream)
				if command != "" {
					_, _ = io.WriteString(stream, "IQuOps-PTY 72 3491\r\n")
					if !scanner.Scan() || scanner.Text() != "" {
						return
					}
				}
				_, _ = fmt.Fprintf(stream, "ready %dx%d", pty.Cols, pty.Rows)
				for scanner.Scan() {
					if scanner.Text() == "exit" {
						_, _ = stream.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
						return
					}
					_, _ = io.WriteString(stream, "echo "+scanner.Text())
				}
			}(input.Command)
		default:
			_ = req.Reply(false, nil)
		}
	}
}

func terminalFixtureAsset(id string, n *terminalNodeFixture) domain.Object {
	return domain.Object{"id": id, "publicHost": "127.0.0.1", "port": n.listener.Addr().(*net.TCPAddr).Port, "sshUser": "operator", "secretAssetIds": []string{id + "_key"}, "sshHostFingerprint": n.fingerprint, "health": domain.Object{"metrics": domain.Object{"swarmNodeId": id, "swarmClusterId": "cluster"}}}
}

func fixtureTerminalSocket(t *testing.T, ctx context.Context, api *testAPI, kind, id, task string) *websocket.Conn {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"kind": kind, "id": id, "taskId": task})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, api.server.URL+"/servers/terminal-tickets", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", api.server.URL)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	var ticket struct{ Ticket, ServerID string }
	if response.StatusCode != 200 || json.Unmarshal(data, &ticket) != nil || ticket.ServerID != "worker" {
		t.Fatalf("终端票据响应不正确，HTTP %d", response.StatusCode)
	}
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(api.server.URL, "http")+"/servers/terminal-stream", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{api.server.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"ticket":"%s","cols":80,"rows":24}`, ticket.Ticket))); err != nil {
		t.Fatal(err)
	}
	return conn
}

func terminalFixtureRead(t *testing.T, ctx context.Context, c *websocket.Conn, want string) {
	t.Helper()
	kind, data, err := c.Read(ctx)
	if err != nil || kind != websocket.MessageBinary || string(data) != want {
		t.Fatal("终端输出与测试响应不符")
	}
}

func TestTerminalWebSocketSSHAcrossNodes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cid := strings.Repeat("a", 64)
	manager := newTerminalNode(t, func(cmd string) (string, bool) {
		switch {
		case strings.HasPrefix(cmd, "docker info "):
			return `{"LocalNodeState":"active","ControlAvailable":true,"Cluster":{"ID":"cluster"}}`, true
		case strings.HasPrefix(cmd, "docker service ls "):
			return "service", strings.Contains(cmd, "iquops.application-id=app")
		case strings.HasPrefix(cmd, "docker service inspect "):
			return `[{"ID":"service","Spec":{"Name":"example","Labels":{"iquops.owner":"iquops","iquops.application-id":"app"},"Mode":{"Replicated":{"Replicas":1}}}}]`, true
		case strings.HasPrefix(cmd, "docker service ps "):
			return "task", true
		case strings.HasPrefix(cmd, "docker inspect --type task "):
			return `[{"ID":"task","NodeID":"worker","Status":{"State":"running"}}]`, true
		}
		return "", false
	})
	var cleaned atomic.Int32
	worker := newTerminalNode(t, func(cmd string) (string, bool) {
		switch {
		case strings.HasPrefix(cmd, "docker ps --no-trunc -q"):
			return cid, strings.Contains(cmd, "task.id=task") && strings.Contains(cmd, "service.id=service") && strings.Contains(cmd, "node.id=worker")
		case cmd == "docker exec "+cid+" /bin/sh -c :":
			return "", true
		case strings.HasPrefix(cmd, "cat >/dev/null; exec docker exec "+cid+" /bin/sh -c ") && strings.HasSuffix(cmd, " -- 72 3491"):
			cleaned.Add(1)
			return "", true
		}
		return "", false
	})
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != worker.listener.Addr().String() {
			w.WriteHeader(403)
			return
		}
		up, err := net.Dial("tcp", r.Host)
		if err != nil {
			w.WriteHeader(502)
			return
		}
		down, b, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = up.Close()
			return
		}
		defer up.Close()
		defer down.Close()
		proxied.Add(1)
		_, _ = b.WriteString("HTTP/1.1 200 OK\r\n\r\n")
		_ = b.Flush()
		go func() { _, _ = io.Copy(up, b); _ = up.Close() }()
		_, _ = io.Copy(down, up)
	}))
	defer proxy.Close()
	api := newTestAPI(t)
	err := api.store.MutateStateWithSecretMaterials(ctx, map[string]string{"manager_ref": manager.privateKey, "worker_ref": worker.privateKey}, func(s *domain.State) error {
		m, w := terminalFixtureAsset("manager", manager), terminalFixtureAsset("worker", worker)
		w["connectionProxy"] = domain.Object{"mode": "manual", "url": proxy.URL}
		s.Servers = []domain.Object{m, w}
		for _, id := range []string{"manager", "worker"} {
			s.SecretAssets = append(s.SecretAssets, domain.Object{"id": id + "_key", "purpose": "deployment_server", "assetType": "ssh_key", "secretRef": id + "_ref"})
		}
		s.Applications = []domain.Object{{"id": "app", "projectId": "project"}}
		s.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "app", "deploymentManagerServerId": "manager", "deploymentClusterId": "cluster"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	conn := fixtureTerminalSocket(t, ctx, api, "application", "app", "task")
	terminalFixtureRead(t, ctx, conn, "ready 80x24")
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	terminalFixtureRead(t, ctx, conn, "echo hello")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"cols":120,"rows":42}`)); err != nil {
		t.Fatal(err)
	}
	terminalFixtureRead(t, ctx, conn, "resize 120x42")
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("exit\n")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(ctx); websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("远端退出状态错误：%v", err)
	}
	conn = fixtureTerminalSocket(t, ctx, api, "server", "worker", "")
	terminalFixtureRead(t, ctx, conn, "ready 80x24")
	_ = conn.CloseNow()
	for worker.stopped.Load() < 2 && ctx.Err() == nil {
		time.Sleep(time.Millisecond * 5)
	}
	if worker.stopped.Load() != 2 || manager.started.Load() != 0 || proxied.Load() != 2 || cleaned.Load() != 1 {
		t.Fatalf("会话未在指定节点收敛：worker=%d manager=%d proxy=%d", worker.stopped.Load(), manager.started.Load(), proxied.Load())
	}
}
