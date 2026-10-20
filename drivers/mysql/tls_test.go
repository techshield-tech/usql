package mysql

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// writeCert writes a self signed certificate and its key to dir, and returns
// the paths.
func writeCert(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "usql test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	write := func(path, typ string, b []byte) {
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b}), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(certPath, "CERTIFICATE", der)
	write(keyPath, "EC PRIVATE KEY", keyDER)
	return certPath, keyPath
}

func TestRegisterTLS(t *testing.T) {
	cert, key := writeCert(t, t.TempDir())
	// the paths of Windows hold characters that a query must escape
	dsn := "u:p@tcp(db:3306)/app?charset=utf8&tls=custom&ssl-ca=" + escape(cert) +
		"&ssl-cert=" + escape(cert) + "&ssl-key=" + escape(key)
	got, err := registerTLS(dsn)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ssl-ca", "ssl-cert", "ssl-key"} {
		if strings.Contains(got, name) {
			t.Errorf("DSN %q still holds %s", got, name)
		}
	}
	cfg, err := mysql.ParseDSN(got)
	if err != nil {
		t.Fatalf("go-sql-driver does not accept %q: %v", got, err)
	}
	if cfg.TLS == nil || cfg.TLS.RootCAs == nil || len(cfg.TLS.Certificates) != 1 {
		t.Errorf("TLS config = %+v, want a CA and one client certificate", cfg.TLS)
	}
	if len(cfg.Params) != 0 {
		t.Errorf("Params = %v, want none, because go-sql-driver sends them to the server", cfg.Params)
	}
}

func TestRegisterTLSWithoutFiles(t *testing.T) {
	// the connector sends the three parameters empty when it has no files
	got, err := registerTLS("u@tcp(db)/?tls=custom&ssl-ca=&ssl-cert=&ssl-key=")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := mysql.ParseDSN(got)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLS == nil {
		t.Error("want TLS on")
	}
}

func TestRegisterTLSUntouched(t *testing.T) {
	for _, dsn := range []string{"u@tcp(db)/app", "u@tcp(db)/app?charset=utf8", "u@tcp(db)/app?tls=true"} {
		if got, err := registerTLS(dsn); err != nil || got != dsn {
			t.Errorf("registerTLS(%q) = %q, %v, want it unchanged", dsn, got, err)
		}
	}
}

func TestRegisterTLSErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(bad, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dsn := range []string{
		"u@tcp(db)/?ssl-ca=" + escape(filepath.Join(dir, "missing.pem")),
		"u@tcp(db)/?ssl-ca=" + escape(bad),
		"u@tcp(db)/?ssl-cert=" + escape(bad) + "&ssl-key=" + escape(bad),
	} {
		if _, err := registerTLS(dsn); err == nil {
			t.Errorf("registerTLS(%q) succeeded, want an error", dsn)
		}
	}
}

func escape(s string) string {
	return strings.NewReplacer("\\", "%5C", ":", "%3A", " ", "%20").Replace(s)
}
