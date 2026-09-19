package shared

import (
	"net/http"
	"testing"

	"github.com/manuellara/ipam/internal/auth"
)

func TestConfigEnabled(t *testing.T) {
	if !auth.ConfigEnabled(1) {
		t.Fatal("configEnabled(1) = false, want true")
	}
	if auth.ConfigEnabled(0) {
		t.Fatal("configEnabled(0) = true, want false")
	}
	if auth.ConfigEnabled(2) {
		t.Fatal("configEnabled(2) = true, want false")
	}
}

func TestRegisterLoginRoutesDoesNotPanicWithNilSecondaryMiddleware(t *testing.T) {
	mux := http.NewServeMux()
	c := &LoginController{}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RegisterLoginRoutes panicked with nil secondary middleware: %v", r)
		}
	}()

	c.RegisterLoginRoutes(mux, func(next http.Handler) http.Handler { return next }, nil)
}
