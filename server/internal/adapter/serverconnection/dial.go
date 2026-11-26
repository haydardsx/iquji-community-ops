package serverconnection

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

func Endpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "\r\n\t ") {
		return nil, errors.New("SERVER_PROXY_ENDPOINT_INVALID")
	}
	switch u.Scheme {
	case "http", "https", "socks5":
	default:
		return nil, errors.New("SERVER_PROXY_ENDPOINT_INVALID")
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return nil, errors.New("SERVER_PROXY_ENDPOINT_INVALID")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return nil, errors.New("SERVER_PROXY_ENDPOINT_INVALID")
	}
	u.Path = ""
	return u, nil
}

type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// SSH 握手和代理协商均受调用方的连接时限约束。
func Dial(ctx context.Context, target string, endpoint *url.URL, credentials *Credentials) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	if endpoint == nil {
		return dialer.DialContext(ctx, "tcp", target)
	}
	port := endpoint.Port()
	if port == "" {
		port = map[string]string{"https": "443", "http": "80", "socks5": "1080"}[endpoint.Scheme]
	}
	proxyAddress := net.JoinHostPort(endpoint.Hostname(), port)
	if endpoint.Scheme == "socks5" {
		var auth *proxy.Auth
		if credentials != nil {
			auth = &proxy.Auth{User: credentials.Username, Password: credentials.Password}
		}
		d, err := proxy.SOCKS5("tcp", proxyAddress, auth, dialer)
		if err != nil {
			return nil, errors.New("SERVER_PROXY_CONNECT_FAILED")
		}
		conn, err := d.(proxy.ContextDialer).DialContext(ctx, "tcp", target)
		if err != nil {
			return nil, errors.New("SERVER_PROXY_CONNECT_FAILED")
		}
		return conn, nil
	}
	conn, err := dialer.DialContext(ctx, "tcp", proxyAddress)
	if err != nil {
		return nil, errors.New("SERVER_PROXY_CONNECT_FAILED")
	}
	raw := conn
	success := false
	defer func() {
		if !success {
			_ = raw.Close()
		}
	}()
	stopCancel := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stopCancel()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if endpoint.Scheme == "https" {
		secure := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: endpoint.Hostname()})
		if err = secure.HandshakeContext(ctx); err != nil {
			return nil, errors.New("SERVER_PROXY_TLS_FAILED")
		}
		conn = secure
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
	if credentials != nil {
		value := base64.StdEncoding.EncodeToString([]byte(credentials.Username + ":" + credentials.Password))
		req.Header.Set("Proxy-Authorization", "Basic "+value)
	}
	if err = req.Write(conn); err != nil {
		return nil, errors.New("SERVER_PROXY_CONNECT_FAILED")
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, req)
	if err != nil {
		return nil, errors.New("SERVER_PROXY_CONNECT_FAILED")
	}
	if response.StatusCode == http.StatusProxyAuthRequired {
		return nil, errors.New("SERVER_PROXY_AUTH_FAILED")
	}
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("SERVER_PROXY_CONNECT_FAILED")
	}
	_ = conn.SetDeadline(time.Time{})
	success = true
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
