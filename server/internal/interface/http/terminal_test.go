package httpapi

import (
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/application"
)

func TestTerminalTicketIsBoundExpiresAndIsSingleUse(t *testing.T) {
	store := terminalTickets{}
	now := time.Now()
	dest := application.TerminalTarget{ApplicationID: "app_a", TaskID: "task_a", ServerID: "node_a"}
	key, err := store.issue(dest, now)
	if err != nil {
		t.Fatal(err)
	}
	var success atomic.Int32
	var users sync.WaitGroup
	for range 12 {
		users.Go(func() {
			if target, ok := store.take(key, now); ok {
				if target != dest {
					t.Error("票据的目标发生变化")
				}
				success.Add(1)
			}
		})
	}
	users.Wait()
	if success.Load() != 1 {
		t.Fatal("同一票据未被唯一消费")
	}
	key, err = store.issue(dest, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.take(key, now.Add(30*time.Second)); ok {
		t.Fatal("已过期票据被接受")
	}
}

func TestTerminalOriginRequiresLocalSameOrigin(t *testing.T) {
	const local = "localhost:4000"
	for _, c := range []struct {
		peer, host, origin string
		want               bool
	}{
		{"127.0.0.1:1234", local, "http://" + local, true},
		{"[::1]:1234", "[::1]:4000", "http://[::1]:4000", true},
		{"127.0.0.1:1234", local, "", false},
		{"127.0.0.1:1234", local, "http://example.invalid", false},
		{"127.0.0.1:1234", local, "http://localhost:4001", false},
		{"127.0.0.1:1234", "example.invalid", "http://example.invalid", false},
		{"192.0.2.3:1234", local, "http://" + local, false},
	} {
		req := httptest.NewRequest("POST", "/servers/terminal-tickets", strings.NewReader("{}"))
		req.Host = c.host
		req.RemoteAddr = c.peer
		req.Header.Set("Origin", c.origin)
		if got := terminalOrigin(req); got != c.want {
			t.Errorf("来源 %q 的校验结果为 %t", c.origin, got)
		}
	}
}
