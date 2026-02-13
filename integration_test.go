// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

//go:build integration

// Package integration_test contains end-to-end integration tests that validate
// the full Traefik ForwardAuth flow using Docker Compose. The test suite spins
// up Traefik v3, the GitHub token validator, a mock GitHub API, and an echo
// upstream service to verify that authentication headers are correctly forwarded.
package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"
)

const (
	// traefikURL is the base URL for the Traefik reverse proxy running in Docker.
	traefikURL = "http://localhost:8888"

	// startupTimeout is the maximum time to wait for all services to be ready.
	startupTimeout = 60 * time.Second

	// pollInterval is how often to check if services are ready during startup.
	pollInterval = 500 * time.Millisecond
)

// echoResponse mirrors the JSON structure returned by the echo upstream service.
type echoResponse struct {
	Headers map[string][]string `json:"headers"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
}

func TestMain(m *testing.M) {
	// Start all services using Docker Compose.
	up := exec.Command("docker", "compose", "up", "--build", "-d")
	up.Dir = "integration"
	up.Stdout = os.Stdout
	up.Stderr = os.Stderr
	if err := up.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "docker compose up failed: %v\n", err)
		os.Exit(1)
	}

	// Wait for Traefik to become ready.
	if err := waitForReady(traefikURL+"/", startupTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "services did not become ready: %v\n", err)
		teardown()
		os.Exit(1)
	}

	// Run tests.
	code := m.Run()

	// Tear down services.
	teardown()

	os.Exit(code)
}

// teardown stops and removes all Docker Compose services.
func teardown() {
	down := exec.Command("docker", "compose", "down", "--volumes", "--remove-orphans")
	down.Dir = "integration"
	down.Stdout = os.Stdout
	down.Stderr = os.Stderr
	if err := down.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "docker compose down failed: %v\n", err)
	}
}

// waitForReady polls the given URL until it responds (any status code) or the
// timeout is reached. During startup, Traefik may return 404 or 502 before
// the ForwardAuth middleware and upstream are fully configured, so we accept
// any HTTP response as a sign that the service is ready.
func waitForReady(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}

	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			// Any response means Traefik is up and routing.
			return nil
		}
		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timed out after %s waiting for %s", timeout, url)
}

func TestNoAuthHeader(t *testing.T) {
	resp, err := http.Get(traefikURL + "/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, resp.StatusCode)
	}
}

func TestValidToken(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, traefikURL+"/", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer valid-test-token-1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var echo echoResponse
	if err := json.NewDecoder(resp.Body).Decode(&echo); err != nil {
		t.Fatalf("failed to decode echo response: %v", err)
	}

	assertHeader(t, echo.Headers, "X-Auth-User-Login", "testuser1")
	assertHeader(t, echo.Headers, "X-Auth-User-Id", "1001")
	assertHeader(t, echo.Headers, "X-Auth-User-Email", "test1@example.com")
	assertHeader(t, echo.Headers, "X-Auth-User-Org", "test-org")
	assertHeader(t, echo.Headers, "X-Auth-User-Teams", "platform-eng,backend")
}

func TestValidToken_DifferentUser(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, traefikURL+"/", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer valid-test-token-2")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var echo echoResponse
	if err := json.NewDecoder(resp.Body).Decode(&echo); err != nil {
		t.Fatalf("failed to decode echo response: %v", err)
	}

	assertHeader(t, echo.Headers, "X-Auth-User-Login", "testuser2")
	assertHeader(t, echo.Headers, "X-Auth-User-Teams", "frontend")
}

func TestInvalidToken(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, traefikURL+"/", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer invalid-token-xyz")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, resp.StatusCode)
	}
}

func TestNonMemberToken(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, traefikURL+"/", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer non-member-token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected status %d, got %d", http.StatusForbidden, resp.StatusCode)
	}
}

func TestClassicPATRejected(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, traefikURL+"/", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer classic-pat-token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected status %d, got %d", http.StatusForbidden, resp.StatusCode)
	}
}

// assertHeader checks that the echo response headers contain the expected
// value for the given header key. Header keys are compared case-insensitively
// per HTTP conventions.
func assertHeader(t *testing.T, headers map[string][]string, key, expected string) {
	t.Helper()

	// Headers in the echo response may have different casing depending on
	// how Go's HTTP server canonicalizes them. Look up using the canonical form.
	values, ok := headers[http.CanonicalHeaderKey(key)]
	if !ok {
		t.Errorf("header %q not found in echo response; available headers: %v", key, headerKeys(headers))
		return
	}

	if len(values) == 0 {
		t.Errorf("header %q has no values", key)
		return
	}

	if values[0] != expected {
		t.Errorf("header %q: expected %q, got %q", key, expected, values[0])
	}
}

// headerKeys returns the keys from a header map for diagnostic output.
func headerKeys(headers map[string][]string) []string {
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	return keys
}
