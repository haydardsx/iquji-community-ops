package application

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 指定镜像时用真实网关检验中间件顺序、Location 和同域 HTTPS 共存。
func TestDomainRedirectOnTraefik(t *testing.T) {
	image := os.Getenv("IQUOPS_TEST_TRAEFIK_IMAGE")
	if image == "" {
		t.Skip("未指定联验镜像")
	}
	dir := t.TempDir()
	write := func(name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	fixture := httptest.NewTLSServer(nil)
	cert := fixture.TLS.Certificates[0]
	fixture.Close()
	der, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	write("cert.pem", certPEM)
	write("cert.key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	inputs := []RouteRuntimeInput{
		{Route: domain.Object{"id": "upgrade"}, Revision: "one", Domains: []domain.Object{{"name": "example.com"}}, Redirect: domain.Object{"targetHost": "example.com", "scheme": "https", "mode": "permanent"}},
		{Route: domain.Object{"id": "alias"}, Revision: "two", Domains: []domain.Object{{"name": "alias.example.test"}}, Redirect: domain.Object{"targetHost": "www.example.test", "scheme": "https", "mode": "temporary"}},
	}
	for _, input := range inputs {
		raw, err := buildDomainRedirectRoute(input)
		if err != nil {
			t.Fatal(err)
		}
		write(stringValue(input.Route["id"])+".yml", raw)
	}
	backend := domain.Object{"http": domain.Object{"routers": domain.Object{"backend": domain.Object{"rule": `Host("example.com")`, "entryPoints": []string{"websecure"}, "service": "ping@internal", "tls": domain.Object{}}}}, "tls": domain.Object{"certificates": []domain.Object{{"certFile": "/fixture/cert.pem", "keyFile": "/fixture/cert.key"}}}}
	raw, err := json.Marshal(backend)
	if err != nil {
		t.Fatal(err)
	}
	write("backend.yml", raw)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", args...).Output()
		if err != nil {
			t.Fatal("Docker 命令失败", args[0], err)
		}
		return strings.TrimSpace(string(out))
	}
	id := cmd("run", "--rm", "-d", "-p", "127.0.0.1::8080", "-p", "127.0.0.1::8443", "--mount", "type=bind,src="+dir+",dst=/fixture,readonly", image,
		"--entrypoints.web.address=:8080", "--entrypoints.websecure.address=:8443", "--providers.file.directory=/fixture", "--ping=true", "--ping.manualrouting=true")
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	web, secure := cmd("port", id, "8080/tcp"), cmd("port", id, "8443/tcp")
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certPEM)
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "example.com", MinVersion: tls.VersionTLS12}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	for _, input := range inputs {
		const uri = "/docs/subpage?filter=a%2Fb&x=1"
		var status int
		for {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+web+uri, nil)
			req.Host = stringValue(input.Domains[0]["name"])
			res, err := client.Do(req)
			if err == nil {
				status = res.StatusCode
				res.Body.Close()
				if verifiedDomainRedirect(input.Redirect, uri, res) && res.Header.Get("X-IQuOps-Route") == routeRevision(input.Route["id"])+":"+input.Revision {
					break
				}
			}
			select {
			case <-ctx.Done():
				t.Fatalf("跳转未生效，最后状态 %d", status)
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+secure+"/ping", nil)
	req.Host = "example.com"
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != 200 || string(body) != "OK" {
		t.Fatalf("HTTPS 业务入口异常 %d", res.StatusCode)
	}
}
