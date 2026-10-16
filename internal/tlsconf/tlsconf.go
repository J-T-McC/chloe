// Package tlsconf builds client TLS settings shared by the drivers.
package tlsconf

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"

	"chloe/internal/model"
)

// Config builds a client TLS configuration from a connection's SSL settings. An
// empty host leaves the name to the driver (MongoDB names each server itself).
func Config(s model.SSLConfig, host string) (*tls.Config, error) {
	tc := &tls.Config{ServerName: host, InsecureSkipVerify: !s.VerifyHost}
	if s.CAFile != "" {
		pem, err := os.ReadFile(s.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("CA file contains no certificates")
		}
		tc.RootCAs = pool
		if !s.VerifyHost {
			// A CA file is always checked: without "verify host" only the name
			// isn't, for servers whose certificate names an internal host.
			tc.VerifyConnection = func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return errors.New("the server sent no certificate")
				}
				inter := x509.NewCertPool()
				for _, c := range cs.PeerCertificates[1:] {
					inter.AddCert(c)
				}
				_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: pool, Intermediates: inter})
				return err
			}
		}
	}
	if s.CertFile != "" && s.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(s.CertFile, s.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	if s.Cipher != "" {
		id, err := cipherID(s.Cipher)
		if err != nil {
			return nil, err
		}
		tc.CipherSuites = []uint16{id}
		tc.MaxVersion = tls.VersionTLS12 // TLS 1.3 suites aren't configurable
	}
	return tc, nil
}

// cipherID accepts Go/IANA names (TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256) and
// OpenSSL-style names (ECDHE-RSA-AES128-GCM-SHA256).
func cipherID(name string) (uint16, error) {
	// Both spellings reduce to the same letters once separators and TLS_/WITH_ are removed.
	norm := func(s string) string {
		return strings.ToUpper(strings.NewReplacer("TLS_", "", "WITH_", "", "_", "", "-", "").Replace(s))
	}
	want := norm(name)
	for _, cs := range append(tls.CipherSuites(), tls.InsecureCipherSuites()...) {
		if norm(cs.Name) == want || cs.Name == name {
			return cs.ID, nil
		}
	}
	return 0, fmt.Errorf("unknown TLS cipher %q", name)
}
