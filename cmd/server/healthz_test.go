package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestHealthzReturns200WithStatusOkWhenPingSucceeds(t *testing.T) {
	handler := healthzHandler(func(context.Context) error { return nil })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if got := body["status"]; got != "ok" {
		t.Errorf(`body["status"] = %q, want "ok"`, got)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestHealthzReturns503WithErrorBodyWhenPingFails(t *testing.T) {
	handler := healthzHandler(func(context.Context) error {
		return errors.New("connection refused")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if got := body["error"]; got != "connection refused" {
		t.Errorf(`body["error"] = %q, want "connection refused"`, got)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestEnvOrDefaultResolvesValueAndFallback(t *testing.T) {
	const key = "BA_TEST_ENV_OR_DEFAULT"

	tests := []struct {
		name     string
		envValue string
		setEnv   bool
		fallback string
		want     string
	}{
		{name: "uses env value when set", envValue: "from-env", setEnv: true, fallback: "fallback", want: "from-env"},
		{name: "uses fallback when unset", setEnv: false, fallback: "fallback", want: "fallback"},
		{name: "uses fallback when set but empty", envValue: "", setEnv: true, fallback: "fallback", want: "fallback"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(key, "") // ensure a clean, restored slot
			if tt.setEnv {
				t.Setenv(key, tt.envValue)
			} else {
				os.Unsetenv(key)
			}

			if got := envOrDefault(key, tt.fallback); got != tt.want {
				t.Errorf("envOrDefault(%q, %q) = %q, want %q", key, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestPortDefaultsTo8080WhenEnvUnset(t *testing.T) {
	os.Unsetenv("PORT")

	if got := port(); got != defaultPort {
		t.Errorf("port() = %q, want %q", got, defaultPort)
	}
	if defaultPort != "8080" {
		t.Errorf("defaultPort = %q, want %q", defaultPort, "8080")
	}
}

func TestPortResolvesFromEnvWhenSet(t *testing.T) {
	t.Setenv("PORT", "9090")

	if got := port(); got != "9090" {
		t.Errorf("port() = %q, want %q", got, "9090")
	}
}
