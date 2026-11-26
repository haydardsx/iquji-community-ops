package application

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func seedJenkinsAccessTest(s *domain.State) error {
	s.Servers = []domain.Object{
		{"id": "build_demo", "health": domain.Object{"metrics": domain.Object{"jenkins": true, "jenkinsVersion": "2.568.1", "jenkinsBundleVersion": "v13-jenkins-2.568.1"}}},
		{"id": "gateway_demo", "health": domain.Object{"metrics": domain.Object{"traefik": true, "traefikHttpsPort": 8443}}},
	}
	s.Domains = []domain.Object{{"id": "domain_demo", "name": "ci.example.test", "certificateId": "cert_demo"}}
	s.Certificates = []domain.Object{{"id": "cert_demo", "domains": []string{"ci.example.test"}, "issuer": "lets_encrypt", "challengeType": "http-01", "resolverId": "resolver_demo"}}
	s.Resolvers = []domain.Object{{"id": "resolver_demo", "ingressId": "ingress_demo"}}
	s.Ingresses = []domain.Object{{"id": "ingress_demo", "serverIds": []string{"gateway_demo"}}}
	return nil
}

func TestJenkinsAccessChecksCertificateAndReservesHost(t *testing.T) {
	r, store, ctx := newTaskTestRuntime(t)
	err := store.MutateState(ctx, seedJenkinsAccessTest)
	if err != nil {
		t.Fatal(err)
	}
	input := JenkinsAccessInput{DomainID: "domain_demo", GatewayServerID: "gateway_demo", ViewerPassword: strings.Repeat("x", 14)}
	task, err := r.CreateJenkinsAccess(ctx, "build_demo", input, false)
	if err != nil || task.Status != "pending_confirmation" {
		t.Fatal("绑定未等待确认", err)
	}
	raw, _ := json.Marshal(task)
	if strings.Contains(string(raw), input.ViewerPassword) {
		t.Fatal("口令进入普通任务")
	}
	if _, err := r.CancelTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := store.HasSecretMaterial(ctx, stringValue(task.Payload["stagedSecretRef"])); ok {
		t.Fatal("取消后保留暂存口令")
	}
	state, _ := store.LoadState(ctx)
	snap, err := r.jenkinsAccessBinding(state, "build_demo", "domain_demo", "gateway_demo")
	if err != nil || snap["url"] != "https://ci.example.test:8443/" {
		t.Fatal("未按实际端口生成 URL", err)
	}
	state.Servers[0]["jenkinsAccess"] = domain.Object{"pending": snap}
	if !jenkinsDomainInUse(state, "domain_demo", "") || !domainAssetHasActiveRoute(state, "certificates", "cert_demo") {
		t.Fatal("丢失候选入口对资产的占用")
	}
	state.Domains[0]["certificateId"] = ""
	if _, err = r.jenkinsAccessBinding(state, "build_demo", "domain_demo", "gateway_demo"); err == nil {
		t.Fatal("允许明文域名")
	}
}

func TestJenkinsAccessForwardUsesMutualTLSAndHTTPSOnly(t *testing.T) {
	ca, server, client, err := jenkinsAccessIdentities()
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(ca))
	for _, entry := range []struct {
		id    accessIdentity
		usage x509.ExtKeyUsage
		host  string
	}{{server, x509.ExtKeyUsageServerAuth, "jenkins-forward.internal"}, {client, x509.ExtKeyUsageClientAuth, ""}} {
		pair, err := tls.X509KeyPair([]byte(entry.id.cert), []byte(entry.id.key))
		if err != nil {
			t.Fatal(err)
		}
		cert, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err = cert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{entry.usage}, DNSName: entry.host}); err != nil {
			t.Fatal(err)
		}
	}
	route := RouteRuntimeInput{Route: domain.Object{"id": "jenkins_demo", "priority": 1001}, Domains: []domain.Object{{"name": "ci.example.test"}}}
	content, err := jenkinsAccessRoute(route, "jenkins-demo", "192.0.2.8", 40000)
	if err != nil || !strings.Contains(string(content), `"entryPoints":["websecure"]`) || !strings.Contains(string(content), `"priority":1001`) || !strings.Contains(string(content), `"url":"https://192.0.2.8:40000"`) {
		t.Fatal("转发路由未限定 HTTPS / mTLS")
	}
}

func TestJenkinsAccessLoginPreservesNativeCSRFAndChecksPermissions(t *testing.T) {
	const password = "example-viewer-password"
	seen := map[string]bool{}
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("from") != "/job/iquops/" {
			t.Error("登录页缺少回跳目标")
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "fixture", Path: "/"})
		fmt.Fprint(w, `<form action="j_spring_security_check"><input type="hidden" name="Jenkins-Crumb" value="fixture&amp;crumb"><input type="text" name="j_username"></form>`)
	})
	mux.HandleFunc("/j_spring_security_check", func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("session"); err != nil || r.FormValue("Jenkins-Crumb") != "fixture&crumb" || r.FormValue("j_username") != "iquops-viewer" || r.FormValue("j_password") != password {
			http.Redirect(w, r, "/loginError", http.StatusFound)
			return
		}
		http.Redirect(w, r, r.FormValue("from"), http.StatusFound)
	})
	mux.HandleFunc("/whoAmI/api/json", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"name":"iquops-viewer","authenticated":true}`)
	})
	for _, path := range []string{"/job/iquops/api/json", "/script", "/job/iquops/config.xml"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			seen[r.URL.Path] = true
			if r.URL.Path != "/job/iquops/api/json" {
				w.WriteHeader(http.StatusForbidden)
			}
		})
	}
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	client := srv.Client()
	client.Jar, _ = cookiejar.New(nil)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	base, _ := url.Parse(srv.URL)
	if err := verifyJenkinsLogin(context.Background(), client, base, password); err != nil || len(seen) != 3 {
		t.Fatal("原生登录或只读权限校验失败", err)
	}
	if err := verifyJenkinsLogin(context.Background(), client, base, "incorrect"); err == nil {
		t.Fatal("错误登录未被拒绝")
	}
}
