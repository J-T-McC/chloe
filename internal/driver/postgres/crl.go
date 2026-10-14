package postgres

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
)

// addCRLCheck rejects server certificates listed in a certificate revocation list.
// pgx has no sslcrl parameter, so the check is added to the TLS handshake.
func addCRLCheck(cfg *pgx.ConnConfig, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read CRL file: %w", err)
	}
	if block := pemBlock(data); block != nil {
		data = block
	}
	crl, err := x509.ParseRevocationList(data)
	if err != nil {
		return fmt.Errorf("parse CRL file: %w", err)
	}
	revoked := map[string]bool{}
	for _, e := range crl.RevokedCertificateEntries {
		revoked[e.SerialNumber.String()] = true
	}
	check := func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		for _, raw := range rawCerts {
			cert, err := x509.ParseCertificate(raw)
			if err != nil {
				return err
			}
			if revoked[cert.SerialNumber.String()] {
				return errors.New("server certificate has been revoked (listed in CRL)")
			}
		}
		return nil
	}
	if cfg.TLSConfig != nil {
		cfg.TLSConfig.VerifyPeerCertificate = chain(cfg.TLSConfig.VerifyPeerCertificate, check)
	}
	for _, fb := range cfg.Fallbacks {
		if fb.TLSConfig != nil {
			fb.TLSConfig.VerifyPeerCertificate = chain(fb.TLSConfig.VerifyPeerCertificate, check)
		}
	}
	return nil
}

type verifyFn = func([][]byte, [][]*x509.Certificate) error

func chain(a, b verifyFn) verifyFn {
	if a == nil {
		return b
	}
	return func(raw [][]byte, chains [][]*x509.Certificate) error {
		if err := a(raw, chains); err != nil {
			return err
		}
		return b(raw, chains)
	}
}

func pemBlock(data []byte) []byte {
	if b, _ := pem.Decode(data); b != nil {
		return b.Bytes
	}
	return nil
}
