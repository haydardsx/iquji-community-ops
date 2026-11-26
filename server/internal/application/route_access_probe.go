package application

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 发布校验经 SSH 连接网关本机入口，同时保留真实 Host、SNI 与系统证书校验。
func verifyRouteAccess(ctx context.Context, client *ssh.Client, input RouteRuntimeInput, removing bool) ([]domain.Object, error) {
	targets, err := routeAccessTargets(input, removing)
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	lastCode := "ROUTE_ACCESS_NOT_VERIFIED"
	for {
		verifications := []domain.Object{}
		complete := true
		for _, target := range targets {
			verified, err := probeRouteAccess(ctx, client, target.input, target.domain, target.rule, removing)
			if err != nil {
				lastCode = stableErrorCode(err, "ROUTE_ACCESS_NOT_VERIFIED")
				complete = false
				break
			}
			verifications = append(verifications, verified)
		}
		if complete {
			return verifications, nil
		}
		select {
		case <-ctx.Done():
			return nil, errors.New(lastCode)
		case <-ticker.C:
		}
	}
}

type routeAccessTarget struct {
	input  RouteRuntimeInput
	domain domain.Object
	rule   domain.Object
}

// 故障时分别验证每个可能快照自己的域名、路径和端口，不混用新旧配置。
func routeAccessTargets(input RouteRuntimeInput, removing bool) ([]routeAccessTarget, error) {
	inputs := []RouteRuntimeInput{input}
	if removing && len(input.PossibleSnapshots) > 0 {
		inputs = nil
		for _, snapshot := range input.PossibleSnapshots {
			possible := input
			possible.Domains, possible.Rules = objectSlice(snapshot["domains"]), objectSlice(snapshot["rules"])
			possible.Revision = stringValue(snapshot["revision"])
			if port := numberValue(snapshot["httpPort"]); port > 0 {
				possible.HTTPPort = port
			}
			if port := numberValue(snapshot["httpsPort"]); port > 0 {
				possible.HTTPSPort = port
			}
			inputs = append(inputs, possible)
		}
	}
	targets := []routeAccessTarget{}
	for _, possible := range inputs {
		if len(possible.Domains) == 0 || len(possible.Rules) == 0 {
			return nil, errors.New("ROUTE_PROBE_TARGET_REQUIRED")
		}
		for _, asset := range possible.Domains {
			for _, rule := range possible.Rules {
				targets = append(targets, routeAccessTarget{input: possible, domain: asset, rule: rule})
				if !possible.HTTPSOnly && (removing || len(input.Redirect) > 0) && asset["tls"] == true {
					plain := cloneObject(asset)
					plain["tls"] = false
					targets = append(targets, routeAccessTarget{input: possible, domain: plain, rule: rule})
				}
			}
		}
	}
	return targets, nil
}

func probeRouteAccess(ctx context.Context, client *ssh.Client, input RouteRuntimeInput, asset, rule domain.Object, removing bool) (domain.Object, error) {
	host, scheme, port := stringValue(asset["name"]), "http", input.HTTPPort
	if port <= 0 {
		port = 80
	}
	if asset["tls"] == true {
		scheme, port = "https", input.HTTPSPort
		if port <= 0 {
			port = 443
		}
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(callCtx context.Context, _, _ string) (net.Conn, error) {
			return client.DialContext(callCtx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		},
		TLSHandshakeTimeout: 8 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: host,
			// 撤销仅验证旧版本标记消失，不把撤销后的默认证书当作发布成功证据。
			InsecureSkipVerify: removing,
		},
	}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	target := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: stringValue(rule["path"])}
	if len(input.Redirect) > 0 {
		target.Path, target.RawQuery = "/.iquops/redirect-check", "check="+input.Revision
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, errors.New("ROUTE_PROBE_REQUEST_INVALID")
	}
	request.Host = host
	response, err := httpClient.Do(request)
	if err != nil {
		if scheme == "https" && !removing {
			return nil, errors.New("ROUTE_TLS_NOT_READY")
		}
		return nil, errors.New("ROUTE_GATEWAY_ACCESS_FAILED")
	}
	defer func() { _ = response.Body.Close() }()
	marker := response.Header.Get("X-IQuOps-Route")
	prefix := routeRevision(input.Route["id"]) + ":"
	matched := marker == prefix+input.Revision
	if removing {
		if strings.HasPrefix(marker, prefix) {
			return nil, errors.New("ROUTE_REVOCATION_NOT_VERIFIED")
		}
	} else {
		if !matched {
			return nil, errors.New("ROUTE_REVISION_NOT_LOADED")
		}
		if len(input.Redirect) > 0 && !verifiedDomainRedirect(input.Redirect, target.RequestURI(), response) {
			return nil, errors.New("DOMAIN_REDIRECT_RESPONSE_INVALID")
		}
		if response.StatusCode >= 500 {
			return nil, errors.New("ROUTE_BACKEND_UNAVAILABLE")
		}
	}
	result := domain.Object{"domain": host, "statusCode": response.StatusCode, "verifiedAt": domain.Now()}
	if !removing && response.TLS != nil && len(response.TLS.PeerCertificates) > 0 {
		result["certificateId"] = asset["certificateId"]
		result["expiresAt"] = response.TLS.PeerCertificates[0].NotAfter.UTC().Format(time.RFC3339)
	}
	return result, nil
}
