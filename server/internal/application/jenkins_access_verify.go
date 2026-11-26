package application

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/net/html"
)

// 校验真实 HTTPS 入口、表单登录、回跳、目录可读及管理员功能拒绝。
func verifyJenkinsAccess(ctx context.Context, gateway *ssh.Client, route RouteRuntimeInput, password string) error {
	host := stringValue(route.Domains[0]["name"])
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(route.HTTPSPort))
	transport := &http.Transport{TLSClientConfig: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return gateway.DialContext(ctx, "tcp", addr) }}
	defer transport.CloseIdleConnections()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Transport: transport, Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	base := &url.URL{Scheme: "https", Host: host}
	if route.HTTPSPort != 443 {
		base.Host = net.JoinHostPort(host, strconv.Itoa(route.HTTPSPort))
	}
	return verifyJenkinsLogin(ctx, c, base, password)
}

func jenkinsLoginForm(page io.Reader) (url.Values, error) {
	tokenizer := html.NewTokenizer(io.LimitReader(page, 1<<20))
	form, inside := url.Values{}, false
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			return nil, errors.New("JENKINS_ACCESS_LOGIN_FAILED")
		}
		token := tokenizer.Token()
		if kind == html.EndTagToken && token.Data == "form" && inside {
			return form, nil
		}
		if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
			continue
		}
		attrs := make(map[string]string, len(token.Attr))
		for _, attr := range token.Attr {
			attrs[attr.Key] = attr.Val
		}
		switch token.Data {
		case "form":
			inside = attrs["action"] == "j_spring_security_check" || attrs["action"] == "/j_spring_security_check"
		case "input":
			if inside && attrs["type"] == "hidden" && attrs["name"] != "" {
				form.Set(attrs["name"], attrs["value"])
			}
		}
	}
}

func verifyJenkinsLogin(ctx context.Context, c *http.Client, base *url.URL, password string) error {
	call := func(method, path string, form url.Values) (*http.Response, error) {
		u, err := base.Parse(path)
		if err != nil {
			return nil, err
		}
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
		if err != nil {
			return nil, err
		}
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		return c.Do(req)
	}
	fail := errors.New("JENKINS_ACCESS_LOGIN_FAILED")
	const folder = "/job/iquops/"
	login, err := call("GET", "/login?from="+url.QueryEscape(folder), nil)
	if err != nil {
		return fail
	}
	form, err := jenkinsLoginForm(login.Body)
	login.Body.Close()
	if login.StatusCode != http.StatusOK || err != nil {
		return fail
	}
	form.Set("from", folder)
	form.Set("j_username", "iquops-viewer")
	form.Set("j_password", password)
	auth, err := call("POST", "/j_spring_security_check", form)
	if err != nil {
		return fail
	}
	auth.Body.Close()
	dest, err := auth.Location()
	if err != nil || auth.StatusCode < 300 || auth.StatusCode >= 400 || dest.Scheme != base.Scheme || dest.Host != base.Host || dest.Path != folder {
		return fail
	}
	who, err := call("GET", "/whoAmI/api/json", nil)
	if err != nil {
		return fail
	}
	var identity struct {
		Name          string
		Authenticated bool
	}
	err = json.NewDecoder(io.LimitReader(who.Body, 1<<16)).Decode(&identity)
	who.Body.Close()
	if err != nil || !identity.Authenticated || identity.Name != "iquops-viewer" {
		return fail
	}
	for _, endpoint := range []struct {
		path   string
		status int
	}{{"/job/iquops/api/json", 200}, {"/script", 403}, {"/job/iquops/config.xml", 403}} {
		res, err := call("GET", endpoint.path, nil)
		if err != nil {
			return fail
		}
		res.Body.Close()
		if res.StatusCode != endpoint.status {
			return fail
		}
	}
	return nil
}
