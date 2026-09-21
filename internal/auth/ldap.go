package auth

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"

	"github.com/go-ldap/ldap/v3"
)

type LDAPUser struct {
	DN    string
	CN    string
	Email string
}

// AuthenticateLDAP performs search-then-bind over implicit TLS (ldaps://).
// Always uses standard certificate validation -- there is no way to
// disable this, by design.
//
// caCertPEM is an optional PEM-encoded CA certificate used to build a
// custom trust pool for verifying the server's certificate. Pass nil (or
// an empty string) to fall back to the system default trust store. This
// is necessary for most real AD/LDAP servers, which are almost always
// signed by an org's internal enterprise CA rather than a publicly
// trusted one.
func AuthenticateLDAP(server string, port int64, baseDN, bindDN, bindPassword, userFilter, username, password string, caCertPEM *string) (*LDAPUser, error) {
	tlsConfig, err := buildLDAPTLSConfig(caCertPEM)
	if err != nil {
		return nil, fmt.Errorf("build TLS config: %w", err)
	}
	return authenticateLDAPWithTLSConfig(server, port, baseDN, bindDN, bindPassword, userFilter, username, password, tlsConfig)
}

// buildLDAPTLSConfig returns a tls.Config trusting the given PEM-encoded
// CA certificate, or a plain tls.Config (system default trust store) if
// caCertPEM is nil or empty.
func buildLDAPTLSConfig(caCertPEM *string) (*tls.Config, error) {
	if caCertPEM == nil || *caCertPEM == "" {
		return &tls.Config{}, nil
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(*caCertPEM)) {
		return nil, fmt.Errorf("failed to parse configured CA certificate")
	}
	return &tls.Config{RootCAs: pool}, nil
}

func authenticateLDAPWithTLSConfig(server string, port int64, baseDN, bindDN, bindPassword, userFilter, username, password string, tlsConfig *tls.Config) (*LDAPUser, error) {
	addr := fmt.Sprintf("ldaps://%s:%d", server, port)
	conn, err := ldap.DialURL(addr, ldap.DialWithTLSConfig(tlsConfig))
	if err != nil {
		return nil, fmt.Errorf("connect to LDAP server: %w", err)
	}
	defer conn.Close()

	if err := conn.Bind(bindDN, bindPassword); err != nil {
		return nil, fmt.Errorf("service account bind failed: %w", err)
	}

	filter := fmt.Sprintf(userFilter, ldap.EscapeFilter(username))
	searchReq := ldap.NewSearchRequest(
		baseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		filter,
		[]string{"dn", "cn", "mail"},
		nil,
	)
	result, err := conn.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("user search failed: %w", err)
	}
	if len(result.Entries) != 1 {
		return nil, fmt.Errorf("expected exactly one match, got %d", len(result.Entries))
	}
	entry := result.Entries[0]

	if err := conn.Bind(entry.DN, password); err != nil {
		return nil, fmt.Errorf("user bind failed: %w", err)
	}

	return &LDAPUser{
		DN:    entry.DN,
		CN:    entry.GetAttributeValue("cn"),
		Email: entry.GetAttributeValue("mail"),
	}, nil
}