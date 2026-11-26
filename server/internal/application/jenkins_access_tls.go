package application

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"time"
)

type accessIdentity struct{ cert, key string }

func jenkinsAccessIdentities() (string, accessIdentity, accessIdentity, error) {
	newCert := func(name string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, nil, err
		}
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			return nil, nil, err
		}
		now := time.Now()
		return &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(5, 0, 0), BasicConstraintsValid: true}, key, nil
	}
	ca, caKey, err := newCert("IQuOps Jenkins access")
	if err != nil {
		return "", accessIdentity{}, accessIdentity{}, err
	}
	ca.IsCA = true
	ca.KeyUsage = x509.KeyUsageCertSign
	raw, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return "", accessIdentity{}, accessIdentity{}, err
	}
	encode := func(kind string, data []byte) string {
		return string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: data}))
	}
	leaf := func(name string, usage x509.ExtKeyUsage) (accessIdentity, error) {
		cert, key, err := newCert(name)
		if err != nil {
			return accessIdentity{}, err
		}
		cert.KeyUsage = x509.KeyUsageDigitalSignature
		cert.ExtKeyUsage = []x509.ExtKeyUsage{usage}
		if usage == x509.ExtKeyUsageServerAuth {
			cert.DNSNames = []string{name}
		}
		der, err := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, caKey)
		if err != nil {
			return accessIdentity{}, err
		}
		secret, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return accessIdentity{}, err
		}
		return accessIdentity{encode("CERTIFICATE", der), encode("PRIVATE KEY", secret)}, nil
	}
	server, err := leaf("jenkins-forward.internal", x509.ExtKeyUsageServerAuth)
	if err != nil {
		return "", accessIdentity{}, accessIdentity{}, err
	}
	client, err := leaf("jenkins-gateway", x509.ExtKeyUsageClientAuth)
	return encode("CERTIFICATE", raw), server, client, err
}
