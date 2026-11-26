package serverconnection

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestProxyEndpointAndTLSVerification(t *testing.T) {
	credentialedProxy := "http://" + url.UserPassword("acct", "pwd").String() + "@proxy.example.test"
	for _, raw := range []string{"file:///secret", credentialedProxy, "https://proxy.example.test/path", "socks5://proxy.example.test:65536", "http://proxy.example.test?x=1", "http://proxy.example.test:"} {
		if _, err := Endpoint(raw); err == nil {
			t.Fatal("代理地址校验接受了无效输入")
		}
	}
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("不应向证书不可信的代理发送请求") }))
	defer s.Close()
	u, _ := url.Parse(s.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := Dial(ctx, "example.test:22", u, &Credentials{Username: "test", Password: "test"}); err == nil || err.Error() != "SERVER_PROXY_TLS_FAILED" {
		t.Fatal(err)
	}
}

func TestSOCKS5PreservesRemoteDNSAndBufferedData(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		header := make([]byte, 2)
		if _, err = io.ReadFull(conn, header); err != nil {
			return
		}
		methods := make([]byte, int(header[1]))
		if _, err = io.ReadFull(conn, methods); err != nil {
			return
		}
		if _, err = conn.Write([]byte{5, 0}); err != nil {
			return
		}
		request := make([]byte, 5)
		if _, err = io.ReadFull(conn, request); err != nil || request[3] != 3 {
			return
		}
		name := make([]byte, int(request[4])+2)
		if _, err = io.ReadFull(conn, name); err != nil {
			return
		}
		received <- string(name[:len(name)-2])
		_, _ = conn.Write(append([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 22}, 'A'))
	}()
	u, _ := Endpoint("socks5://" + listener.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := Dial(ctx, "remote.example.test:22", u, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var data [1]byte
	if _, err := io.ReadFull(conn, data[:]); err != nil || data[0] != 'A' {
		t.Fatal("代理连接返回的数据不符")
	}
	if host := <-received; host != "remote.example.test" {
		t.Fatal("代理解析的目标不符")
	}
}
