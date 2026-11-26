package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/haydardsx/iquji-community-ops/server/internal/application"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type terminalTicket struct {
	target  application.TerminalTarget
	expires time.Time
}

type terminalTickets struct {
	mu     sync.Mutex
	items  map[string]terminalTicket
	active int
}

func (t *terminalTickets) issue(target application.TerminalTarget, now time.Time) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.items == nil {
		t.items = make(map[string]terminalTicket)
	}
	for k, v := range t.items {
		if !v.expires.After(now) {
			delete(t.items, k)
		}
	}
	if len(t.items) >= 64 {
		return "", errors.New("TERMINAL_LIMIT_REACHED")
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(key[:])
	t.items[token] = terminalTicket{target: target, expires: now.Add(30 * time.Second)}
	return token, nil
}

func (t *terminalTickets) take(key string, now time.Time) (application.TerminalTarget, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.items[key]
	delete(t.items, key)
	return v.target, ok && v.expires.After(now)
}

// 回环 Host 与来源同时校验，开发代理保留浏览器原 Host。
func terminalOrigin(r *http.Request) bool {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(peer).IsLoopback() {
		return false
	}
	u, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Host != r.Host || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()
}

func (s *Server) handleTerminalTicket(w http.ResponseWriter, r *http.Request) {
	if !terminalOrigin(r) {
		writeError(w, r, 403, "TERMINAL_ORIGIN_DENIED")
		return
	}
	var input struct {
		Kind   string `json:"kind"`
		ID     string `json:"id"`
		TaskID string `json:"taskId,omitempty"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if !decodeStrict(w, r, &input) {
		return
	}
	target, err := s.runtime.ResolveTerminalTarget(r.Context(), input.Kind, input.ID, input.TaskID)
	if err != nil {
		writeResult(w, r, nil, err)
		return
	}
	token, err := s.terminals.issue(target, time.Now())
	w.Header().Set("Cache-Control", "no-store")
	writeResult(w, r, map[string]string{"ticket": token, "serverId": target.ServerID}, err)
}

type terminalSize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

func (v terminalSize) valid() bool {
	return v.Cols > 0 && v.Cols <= 1000 && v.Rows > 0 && v.Rows <= 500
}

type terminalOutput struct {
	ctx  context.Context
	conn *websocket.Conn
}

func (w terminalOutput) Write(data []byte) (int, error) {
	ctx, cancel := context.WithTimeout(w.ctx, 15*time.Second)
	defer cancel()
	if err := w.conn.Write(ctx, websocket.MessageBinary, data); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (s *Server) handleTerminalStream(w http.ResponseWriter, r *http.Request) {
	if !terminalOrigin(r) {
		writeError(w, r, 403, "TERMINAL_ORIGIN_DENIED")
		return
	}
	s.terminals.mu.Lock()
	if s.terminals.active >= 16 {
		s.terminals.mu.Unlock()
		writeError(w, r, 429, "TERMINAL_LIMIT_REACHED")
		return
	}
	s.terminals.active++
	s.terminals.mu.Unlock()
	defer func() { s.terminals.mu.Lock(); s.terminals.active--; s.terminals.mu.Unlock() }()
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(64 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	setup, done := context.WithTimeout(ctx, 15*time.Second)
	defer done()
	kind, data, err := conn.Read(setup)
	var hello struct {
		Ticket string `json:"ticket"`
		terminalSize
	}
	if err != nil || kind != websocket.MessageText || json.Unmarshal(data, &hello) != nil || !hello.valid() {
		_ = conn.Close(websocket.StatusPolicyViolation, "TERMINAL_HANDSHAKE_INVALID")
		return
	}
	target, ok := s.terminals.take(hello.Ticket, time.Now())
	if !ok {
		_ = conn.Close(websocket.StatusPolicyViolation, "TERMINAL_TICKET_EXPIRED")
		return
	}
	if target.ApplicationID != "" {
		live, err := s.runtime.ResolveTerminalTarget(setup, "application", target.ApplicationID, target.TaskID)
		if err != nil || live != target {
			_ = conn.Close(websocket.StatusPolicyViolation, "TERMINAL_TASK_CHANGED")
			return
		}
	}
	session, err := s.runtime.OpenTerminal(setup, target, hello.Cols, hello.Rows, terminalOutput{ctx, conn})
	if err != nil {
		code := applicationTerminalError(err)
		_ = conn.Close(websocket.StatusPolicyViolation, code)
		return
	}
	done()
	defer session.Close()
	exited := make(chan error, 1)
	go func() { exited <- session.Wait() }()
	disconnected := make(chan error, 1)
	go func() { disconnected <- readTerminalInput(ctx, conn, session) }()
	select {
	case err = <-exited:
		if err != nil {
			_ = conn.Close(websocket.StatusGoingAway, "TERMINAL_SESSION_ENDED")
		} else {
			_ = conn.Close(websocket.StatusNormalClosure, "")
		}
	case err = <-disconnected:
		if err != nil {
			_ = conn.Close(websocket.StatusPolicyViolation, "TERMINAL_DISCONNECTED")
		}
	case <-ctx.Done():
	}
}

func applicationTerminalError(err error) string {
	code := err.Error()
	for _, prefix := range []string{"TERMINAL_", "SSH_", "SERVER_"} {
		if strings.HasPrefix(code, prefix) && domain.IsPublicDiagnosticCode(code) {
			return code
		}
	}
	return "TERMINAL_CONNECTION_FAILED"
}

func readTerminalInput(ctx context.Context, conn *websocket.Conn, session *application.TerminalSession) error {
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		if kind == websocket.MessageBinary {
			if _, err = session.Input.Write(data); err != nil {
				return err
			}
		} else {
			var size terminalSize
			if json.Unmarshal(data, &size) != nil || !size.valid() {
				return errors.New("TERMINAL_SIZE_INVALID")
			}
			if err = session.Resize(size.Cols, size.Rows); err != nil {
				return err
			}
		}
	}
}
