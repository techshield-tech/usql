package mysql

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// tlsName is the name of the TLS configuration that registerTLS registers. A
// connection asks for it with tls=custom.
const tlsName = "custom"

// registerTLS reads the ssl-ca, ssl-cert and ssl-key parameters of a DSN, and
// registers a TLS configuration from the files that they name. go-sql-driver
// has no such parameters. It would send them to the server as system
// variables, so registerTLS removes them, and returns a DSN that asks for the
// registered configuration with tls=custom.
//
// A DSN that names none of the three and does not ask for tls=custom is
// returned as it is.
func registerTLS(dsn string) (string, error) {
	i := strings.LastIndex(dsn, "?")
	if i < 0 {
		return dsn, nil
	}
	query, err := url.ParseQuery(dsn[i+1:])
	if err != nil {
		return "", fmt.Errorf("parsing mysql DSN parameters: %w", err)
	}
	ca, cert, key := query.Get("ssl-ca"), query.Get("ssl-cert"), query.Get("ssl-key")
	if !query.Has("ssl-ca") && !query.Has("ssl-cert") && !query.Has("ssl-key") {
		return dsn, nil
	}
	query.Del("ssl-ca")
	query.Del("ssl-cert")
	query.Del("ssl-key")
	cfg := new(tls.Config)
	if ca != "" {
		pem, err := os.ReadFile(ca)
		if err != nil {
			return "", fmt.Errorf("reading ssl-ca: %w", err)
		}
		cfg.RootCAs = x509.NewCertPool()
		if !cfg.RootCAs.AppendCertsFromPEM(pem) {
			return "", fmt.Errorf("reading ssl-ca %s: no certificate found", ca)
		}
	}
	if cert != "" || key != "" {
		pair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return "", fmt.Errorf("loading ssl-cert and ssl-key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	if err := mysql.RegisterTLSConfig(tlsName, cfg); err != nil {
		return "", fmt.Errorf("registering the TLS configuration: %w", err)
	}
	query.Set("tls", tlsName)
	return dsn[:i] + "?" + query.Encode(), nil
}
