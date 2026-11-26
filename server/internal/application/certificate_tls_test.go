package application

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"testing"
	"time"
)

func TestCertificateProbeRequestsOneCertificateCoveringAllDomains(t *testing.T) {
	hosts := []string{"api.example.test", "web.example.test"}
	content, err := buildCertificateProbeConfig("certificate_probe_one", CertificateVerifyInput{Domains: hosts, ResolverName: "managed", HTTPPort: 80})
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		HTTP struct {
			Routers map[string]struct {
				TLS struct {
					Domains []struct {
						Main string
						Sans []string
					}
				}
			}
		}
	}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.HTTP.Routers) != 2 {
		t.Fatal("每个主机名都需要触发 TLS 路由")
	}
	for _, name := range []string{"certificate_probe_one-0", "certificate_probe_one-1"} {
		router, ok := config.HTTP.Routers[name]
		if !ok || len(router.TLS.Domains) != 1 || router.TLS.Domains[0].Main != hosts[0] || len(router.TLS.Domains[0].Sans) != 1 || router.TLS.Domains[0].Sans[0] != hosts[1] {
			t.Fatal("多域名申请未形成相同 SAN 集合或路由名称冲突")
		}
	}
}

func TestCertificateTLSRejectsUntrustedExpiredAndWrongHostname(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "临时测试 CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, public, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	for _, tc := range []struct {
		name, host               string
		expired, trust, succeeds bool
	}{
		{"有效链", "api.example.test", false, true, true},
		{"错误主机名", "other.example.test", false, true, false},
		{"过期", "api.example.test", true, true, false},
		{"不可信根", "api.example.test", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "api.example.test"}, DNSNames: []string{"api.example.test"}, NotBefore: time.Now().Add(-2 * time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
			if tc.expired {
				leaf.NotAfter = time.Now().Add(-time.Hour)
			}
			der, err := x509.CreateCertificate(rand.Reader, leaf, ca, public, key)
			if err != nil {
				t.Fatal(err)
			}
			serverRaw, clientRaw := net.Pipe()
			defer serverRaw.Close()
			defer clientRaw.Close()
			server := tls.Server(serverRaw, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der, caDER}, PrivateKey: key}}})
			config := certificateTLSConfig(tc.host)
			if tc.trust {
				config.RootCAs = pool
			}
			client := tls.Client(clientRaw, config)
			_ = server.SetDeadline(time.Now().Add(3 * time.Second))
			_ = client.SetDeadline(time.Now().Add(3 * time.Second))
			done := make(chan error, 1)
			go func() { done <- server.Handshake() }()
			err = client.Handshake()
			_ = clientRaw.Close()
			<-done
			if (err == nil) != tc.succeeds {
				t.Fatalf("证书验证结果不正确：%v", err)
			}
		})
	}
}
