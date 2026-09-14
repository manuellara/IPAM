package session

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/manuellara/ipam/internal/database"
)

func TestNewPersistsSessions(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "ipam.db")
	databaseService, err := database.New(databasePath)
	if err != nil {
		t.Fatalf("database.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := databaseService.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	manager := New(databaseService.DB())
	setValue := manager.LoadAndSave(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		manager.Put(request.Context(), "user_id", int64(42))
	}))

	firstResponse := httptest.NewRecorder()
	setValue.ServeHTTP(firstResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	if len(firstResponse.Result().Cookies()) != 1 {
		t.Fatalf("session cookie count = %d, want 1", len(firstResponse.Result().Cookies()))
	}

	getValue := manager.LoadAndSave(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if userID := manager.GetInt64(request.Context(), "user_id"); userID != 42 {
			t.Errorf("user ID = %d, want 42", userID)
		}
	}))

	secondRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	secondRequest.AddCookie(firstResponse.Result().Cookies()[0])
	getValue.ServeHTTP(httptest.NewRecorder(), secondRequest)
}
