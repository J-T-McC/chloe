package tlsconf

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chloe/internal/model"
)

func TestCAFileIsCheckedWithoutVerifyHost(t *testing.T) {
	srv := httptest.NewTLSServer(nil)
	defer srv.Close()
	other := httptest.NewUnstartedServer(nil)
	other.TLS = &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}}
	other.StartTLS()
	defer other.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	dial := func(addr string) error {
		tc, err := Config(model.SSLConfig{Enabled: true, CAFile: ca}, "db.internal")
		if err != nil {
			return err
		}
		c, err := tls.Dial("tcp", addr, tc)
		if err == nil {
			c.Close()
		}
		return err
	}
	if err := dial(strings.TrimPrefix(srv.URL, "https://")); err != nil {
		t.Fatalf("the CA's server, under another name: %v", err)
	}
	if err := dial(strings.TrimPrefix(other.URL, "https://")); err == nil {
		t.Fatal("a server the CA didn't sign was accepted")
	}
}

func selfSigned(t *testing.T) tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
