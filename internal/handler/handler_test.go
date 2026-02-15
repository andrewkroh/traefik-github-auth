// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrewkroh/traefik-github-auth/internal/validator"
)

// mockValidator implements TokenValidator for testing.
type mockValidator struct {
	validateFunc func(ctx context.Context, token string) (*validator.ValidationResult, error)
}

func (m *mockValidator) Validate(ctx context.Context, token string) (*validator.ValidationResult, error) {
	return m.validateFunc(ctx, token)
}

func newTestHandler(mv *mockValidator) http.Handler {
	log := slog.Default()
	h := New(mv, log)
	return h.Routes()
}

func TestValidate_MissingAuthHeader(t *testing.T) {
	handler := newTestHandler(&mockValidator{
		validateFunc: func(_ context.Context, _ string) (*validator.ValidationResult, error) {
			t.Fatal("validator should not be called when auth header is missing")
			return nil, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error == "" {
		t.Fatal("expected non-empty error message")
	}

	if got := resp.Error; got != "missing or malformed Authorization header" {
		t.Fatalf("expected error %q, got %q", "missing or malformed Authorization header", got)
	}
}

func TestValidate_MalformedAuthHeader_NoBearer(t *testing.T) {
	handler := newTestHandler(&mockValidator{
		validateFunc: func(_ context.Context, _ string) (*validator.ValidationResult, error) {
			t.Fatal("validator should not be called with malformed auth header")
			return nil, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.Header.Set("Authorization", "Basic xxx")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error != "missing or malformed Authorization header" {
		t.Fatalf("expected error %q, got %q", "missing or malformed Authorization header", resp.Error)
	}
}

func TestValidate_MalformedAuthHeader_BearerOnly(t *testing.T) {
	handler := newTestHandler(&mockValidator{
		validateFunc: func(_ context.Context, _ string) (*validator.ValidationResult, error) {
			t.Fatal("validator should not be called with empty bearer token")
			return nil, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error != "missing or malformed Authorization header" {
		t.Fatalf("expected error %q, got %q", "missing or malformed Authorization header", resp.Error)
	}
}

func TestValidate_Success(t *testing.T) {
	handler := newTestHandler(&mockValidator{
		validateFunc: func(_ context.Context, token string) (*validator.ValidationResult, error) {
			if token != "test-token" {
				t.Fatalf("expected token %q, got %q", "test-token", token)
			}
			return &validator.ValidationResult{
				Login: "octocat",
				ID:    12345,
				Org:   "test-org",
				Teams: []string{"team-a", "team-b"},
			}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	if got := rec.Header().Get("X-Auth-User-Login"); got != "octocat" {
		t.Fatalf("expected X-Auth-User-Login %q, got %q", "octocat", got)
	}

	if got := rec.Header().Get("X-Auth-User-Id"); got != "12345" {
		t.Fatalf("expected X-Auth-User-Id %q, got %q", "12345", got)
	}

	if got := rec.Header().Get("X-Auth-User-Org"); got != "test-org" {
		t.Fatalf("expected X-Auth-User-Org %q, got %q", "test-org", got)
	}

	if got := rec.Header().Get("X-Auth-User-Teams"); got != "team-a,team-b" {
		t.Fatalf("expected X-Auth-User-Teams %q, got %q", "team-a,team-b", got)
	}
}

func TestValidate_Unauthorized(t *testing.T) {
	handler := newTestHandler(&mockValidator{
		validateFunc: func(_ context.Context, _ string) (*validator.ValidationResult, error) {
			return nil, fmt.Errorf("%w", validator.ErrUnauthorized)
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error != "access denied" {
		t.Fatalf("expected error %q, got %q", "access denied", resp.Error)
	}
}

func TestValidate_NotOrgMember(t *testing.T) {
	handler := newTestHandler(&mockValidator{
		validateFunc: func(_ context.Context, _ string) (*validator.ValidationResult, error) {
			return nil, fmt.Errorf("%w", validator.ErrNotOrgMember)
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error != "access denied" {
		t.Fatalf("expected error %q, got %q", "access denied", resp.Error)
	}
}

func TestValidate_ClassicPAT(t *testing.T) {
	handler := newTestHandler(&mockValidator{
		validateFunc: func(_ context.Context, _ string) (*validator.ValidationResult, error) {
			return nil, fmt.Errorf("%w", validator.ErrClassicPAT)
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error != "forbidden: classic PATs are not allowed" {
		t.Fatalf("expected error %q, got %q", "forbidden: classic PATs are not allowed", resp.Error)
	}
}

func TestValidate_InternalError(t *testing.T) {
	handler := newTestHandler(&mockValidator{
		validateFunc: func(_ context.Context, _ string) (*validator.ValidationResult, error) {
			return nil, errors.New("some unexpected database failure")
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error != "internal server error" {
		t.Fatalf("expected error %q, got %q", "internal server error", resp.Error)
	}

	// Ensure the original error message is not leaked.
	body := rec.Body.String()
	if containsString(body, "database") {
		t.Fatal("response body should not contain internal error details")
	}
}

func TestHealthz(t *testing.T) {
	handler := newTestHandler(&mockValidator{})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	if body := rec.Body.String(); body != "ok" {
		t.Fatalf("expected body %q, got %q", "ok", body)
	}
}

func TestReady(t *testing.T) {
	handler := newTestHandler(&mockValidator{})

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	if body := rec.Body.String(); body != "ok" {
		t.Fatalf("expected body %q, got %q", "ok", body)
	}
}

func TestValidate_EmptyTeams(t *testing.T) {
	handler := newTestHandler(&mockValidator{
		validateFunc: func(_ context.Context, _ string) (*validator.ValidationResult, error) {
			return &validator.ValidationResult{
				Login: "octocat",
				ID:    12345,
				Org:   "test-org",
				Teams: []string{},
			}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	if got := rec.Header().Get("X-Auth-User-Teams"); got != "" {
		t.Fatalf("expected X-Auth-User-Teams to be empty, got %q", got)
	}
}

func TestValidate_MultipleTeams(t *testing.T) {
	handler := newTestHandler(&mockValidator{
		validateFunc: func(_ context.Context, _ string) (*validator.ValidationResult, error) {
			return &validator.ValidationResult{
				Login: "octocat",
				ID:    12345,
				Org:   "test-org",
				Teams: []string{"engineering", "security", "platform"},
			}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	expected := "engineering,security,platform"
	if got := rec.Header().Get("X-Auth-User-Teams"); got != expected {
		t.Fatalf("expected X-Auth-User-Teams %q, got %q", expected, got)
	}
}

func TestGetSourceIP_XForwardedFor_SingleIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.RemoteAddr = "10.0.0.5:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.42")

	ip := getSourceIP(req)

	expected := "203.0.113.42"
	if ip != expected {
		t.Fatalf("expected source IP %q, got %q", expected, ip)
	}
}

func TestGetSourceIP_XForwardedFor_MultipleIPs(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.RemoteAddr = "10.0.0.5:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.42, 198.51.100.1, 192.0.2.1")

	ip := getSourceIP(req)

	// Should return the first (leftmost) IP, which is the original client.
	expected := "203.0.113.42"
	if ip != expected {
		t.Fatalf("expected source IP %q, got %q", expected, ip)
	}
}

func TestGetSourceIP_NoXForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.RemoteAddr = "10.0.0.5:12345"

	ip := getSourceIP(req)

	// Should fall back to RemoteAddr (without the port).
	expected := "10.0.0.5"
	if ip != expected {
		t.Fatalf("expected source IP %q, got %q", expected, ip)
	}
}

func TestGetSourceIP_XForwardedFor_WithSpaces(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.RemoteAddr = "10.0.0.5:12345"
	req.Header.Set("X-Forwarded-For", "  203.0.113.42  ,  198.51.100.1  ")

	ip := getSourceIP(req)

	// Should trim whitespace from the first IP.
	expected := "203.0.113.42"
	if ip != expected {
		t.Fatalf("expected source IP %q, got %q", expected, ip)
	}
}

func TestGetSourceIP_XForwardedFor_Empty(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.RemoteAddr = "10.0.0.5:12345"
	req.Header.Set("X-Forwarded-For", "")

	ip := getSourceIP(req)

	// Empty X-Forwarded-For should fall back to RemoteAddr.
	expected := "10.0.0.5"
	if ip != expected {
		t.Fatalf("expected source IP %q, got %q", expected, ip)
	}
}

func TestValidate_RateLimited(t *testing.T) {
	handler := newTestHandler(&mockValidator{
		validateFunc: func(_ context.Context, _ string) (*validator.ValidationResult, error) {
			return nil, fmt.Errorf("%w", validator.ErrRateLimited)
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status %d, got %d", http.StatusTooManyRequests, rec.Code)
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Error != "rate limit exceeded, try again later" {
		t.Fatalf("expected error %q, got %q", "rate limit exceeded, try again later", resp.Error)
	}
}

func TestValidate_HeaderInjection_Login(t *testing.T) {
	handler := newTestHandler(&mockValidator{
		validateFunc: func(_ context.Context, _ string) (*validator.ValidationResult, error) {
			t.Fatal("validator should not be called when auth headers are injected")
			return nil, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	req.Header.Set("X-Auth-User-Login", "admin")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Error != "forbidden: request contains disallowed headers" {
		t.Fatalf("expected error %q, got %q", "forbidden: request contains disallowed headers", resp.Error)
	}
}

func TestValidate_HeaderInjection_Teams(t *testing.T) {
	handler := newTestHandler(&mockValidator{
		validateFunc: func(_ context.Context, _ string) (*validator.ValidationResult, error) {
			t.Fatal("validator should not be called when auth headers are injected")
			return nil, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/validate", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	req.Header.Set("X-Auth-User-Teams", "admin-team")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
}

// containsString is a simple helper to check if a string contains a substring.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
