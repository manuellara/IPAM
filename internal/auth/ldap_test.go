package auth

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"testing"
)

// testTLSConfig loads the test CA certificate and returns a TLS configuration
// that trusts it. This is used for testing LDAP over LDAPS with a local
// test server.
func testTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	caCert, err := os.ReadFile("../../test/ldap-bootstrap/certs/ca.crt")
	if err != nil {
		t.Fatalf("read test CA cert: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		t.Fatal("failed to parse test CA cert")
	}
	return &tls.Config{RootCAs: pool}
}

func TestAuthenticateLDAP_Success(t *testing.T) {
	tlsConfig := testTLSConfig(t)

	user, err := authenticateLDAPWithTLSConfig(
		"localhost", 636,
		"ou=people,dc=ipam,dc=test",
		"cn=ipam,ou=service-accounts,dc=ipam,dc=test",
		"ipam-bind-password",
		"(uid=%s)",
		"testuser", "test-password",
		tlsConfig,
	)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if user.CN != "Test User" {
		t.Errorf("expected CN 'Test User', got %q", user.CN)
	}
}

func TestAuthenticateLDAP_WrongPassword(t *testing.T) {
	tlsConfig := testTLSConfig(t)

	_, err := authenticateLDAPWithTLSConfig(
		"localhost", 636,
		"ou=people,dc=ipam,dc=test",
		"cn=ipam,ou=service-accounts,dc=ipam,dc=test",
		"ipam-bind-password",
		"(uid=%s)",
		"testuser", "wrong-password",
		tlsConfig,
	)
	if err == nil {
		t.Fatal("expected an error for wrong password, got nil")
	}
}

func TestAuthenticateLDAP_WithCACert(t *testing.T) {
	caCertBytes, err := os.ReadFile("../../test/ldap-bootstrap/certs/ca.crt")
	if err != nil {
		t.Fatalf("read test CA cert: %v", err)
	}
	caCertPEM := string(caCertBytes)

	user, err := AuthenticateLDAP(
		"localhost", 636,
		"ou=people,dc=ipam,dc=test",
		"cn=ipam,ou=service-accounts,dc=ipam,dc=test",
		"ipam-bind-password",
		"(uid=%s)",
		"testuser", "test-password",
		&caCertPEM,
	)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if user.CN != "Test User" {
		t.Errorf("expected CN 'Test User', got %q", user.CN)
	}
}