package application

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestServerConnectionTrustAndProxy(t *testing.T) {
	r, store, ctx := newTaskTestRuntime(t)
	_, server, key := runtimeProbeSSH(t, func(string) (string, uint32) { return "ok", 0 })
	id := stringValue(server["id"])
	if err := store.MutateState(ctx, func(s *domain.State) error { s.Servers = append(s.Servers, server); return nil }); err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodConnect || req.Header.Get("Proxy-Authorization") != "Basic dGVzdDp0ZXN0" {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		up, err := net.Dial("tcp", req.Host)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		conn, b, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = up.Close()
			return
		}
		defer conn.Close()
		defer up.Close()
		_, _ = b.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
		_ = b.Flush()
		go func() { _, _ = io.Copy(up, b); _ = up.Close() }()
		_, _ = io.Copy(conn, up)
	}))
	defer proxy.Close()
	input := ServerConnectionInput{Mode: "manual", URL: proxy.URL}
	input.Credentials.Action = "replace"
	input.Credentials.Username = "test"
	input.Credentials.Password = "test"
	if _, err := r.SaveServerConnection(ctx, id, input); err != nil {
		t.Fatal(err)
	}
	s, _ := store.LoadState(ctx)
	current := findObject(s.Servers, id)
	if _, err := sshClient(r.WithServerConnections(ctx), current, key); err == nil || err.Error() != "SSH_HOST_KEY_TRUST_REQUIRED" {
		t.Fatal(err)
	}
	result, err := r.CheckServerConnection(ctx, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	fp := stringValue(result["fingerprint"])
	if !strings.HasPrefix(fp, "SHA256:") || result["trusted"] != false {
		t.Fatal("未标记新的 SSH 主机")
	}
	fake := "SHA256:unverified"
	if _, err = r.CheckServerConnection(ctx, id, &fake); err == nil {
		t.Fatal("允许信任不匹配的指纹")
	}
	if _, err = r.CheckServerConnection(ctx, id, &fp); err != nil {
		t.Fatal(err)
	}
	s, _ = store.LoadState(ctx)
	current = findObject(s.Servers, id)
	conn, err := r.openTerminalSSH(ctx, current, key)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	current["sshHostFingerprint"] = fake
	if _, err = sshClient(r.WithServerConnections(ctx), current, key); err == nil || err.Error() != "SSH_HOST_KEY_CHANGED" {
		t.Fatal(err)
	}
	archive := domain.BuildBackupPayload(s, "1", []domain.BackupScope{domain.BackupScopeServers}, false)
	data, _ := json.Marshal(archive)
	if strings.Contains(string(data), `"password"`) || strings.Contains(string(data), "server-proxy:") || !strings.Contains(string(data), proxy.URL) {
		t.Fatal("服务器归档未正确分离代理配置和材料")
	}
	input.Credentials.Action = "keep"
	input.Credentials.Username = ""
	input.Credentials.Password = ""
	input.URL = "http://proxy.example.test"
	if _, err = r.SaveServerConnection(ctx, id, input); err == nil || err.Error() != "SERVER_PROXY_NEW_CREDENTIALS_REQUIRED" {
		t.Fatal(err)
	}
	input.URL = proxy.URL
	input.Credentials.Action = "replace"
	input.Credentials.Username = "test"
	input.Credentials.Password = "wrong"
	if _, err = r.SaveServerConnection(ctx, id, input); err != nil {
		t.Fatal(err)
	}
	if _, err = r.CheckServerConnection(ctx, id, nil); err == nil || err.Error() != "SERVER_PROXY_AUTH_FAILED" {
		t.Fatal(err)
	}
	input = ServerConnectionInput{Mode: "direct"}
	input.Credentials.Action = "clear"
	if _, err = r.SaveServerConnection(ctx, id, input); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.HasSecretMaterial(ctx, proxyMaterialRef(id)); err != nil || ok {
		t.Fatal("密码清理失败")
	}
	result, err = r.CheckServerConnection(ctx, id, nil)
	if err != nil || result["trusted"] != true {
		t.Fatal("连接结果不符合预期")
	}
}

func TestServerConnectionMigrationAndFrpc(t *testing.T) {
	raw := []byte(`{"servers":[{"id":"node"}]}`)
	state, err := domain.NormalizeState(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []domain.State{state, backupPayloadState(domain.BackupPayload{Servers: &domain.BackupServersData{Servers: []domain.Object{{"id": "node"}}}}, []domain.BackupScope{domain.BackupScopeServers}, false)} {
		server := findObject(s.Servers, "node")
		if objectMapFromAny(server["connectionProxy"])["mode"] != "direct" {
			t.Fatal(server)
		}
		if u, _, err := connectionProxy(context.Background(), server); err != nil || u != nil {
			t.Fatal("代理设置读取结果不符")
		}
	}
	env := []string{"http_proxy=stale", "HTTPS_PROXY=stale", "ALL_PROXY=stale", "NO_PROXY=stale", "SKIP=ok"}
	got := strings.Join(frpcEnvironment(env, FrpTunnelExecutionInput{}), " ")
	if got != "SKIP=ok FRP_TOKEN=" {
		t.Fatal(got)
	}
	got = strings.Join(frpcEnvironment(env, FrpTunnelExecutionInput{proxyURL: "socks5://proxy.example.test:1080"}), " ")
	if !strings.Contains(got, "http_proxy=socks5://proxy.example.test:1080") || strings.Contains(got, "stale") {
		t.Fatal(got)
	}
	credentialedProxy := "http://" + url.UserPassword("user", "pass").String() + "@proxy.example.test"
	if err := validateArchivedServerConnection(domain.Object{"connectionProxy": domain.Object{"mode": "manual", "url": credentialedProxy}}); err == nil {
		t.Fatal("归档允许内联认证")
	}
}
