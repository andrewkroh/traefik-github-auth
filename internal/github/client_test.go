// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testToken = "test-token-for-unit-tests"

// TestHTTPClient_ImplementsInterface is a compile-time check that
// *HTTPClient satisfies the Client interface.
var _ Client = (*HTTPClient)(nil)

func TestHTTPClient_GetUser_Success(t *testing.T) {
	user := User{Login: "octocat", ID: 1, Email: "octocat@github.com"}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(user)
	}))
	defer srv.Close()

	client := NewHTTPClient(WithBaseURL(srv.URL))
	got, isClassic, err := client.GetUser(context.Background(), testToken)
	if err != nil {
		t.Fatalf("GetUser returned error: %v", err)
	}
	if isClassic {
		t.Error("expected isClassicPAT=false, got true")
	}
	if got.Login != user.Login {
		t.Errorf("Login: got %q, want %q", got.Login, user.Login)
	}
	if got.ID != user.ID {
		t.Errorf("ID: got %d, want %d", got.ID, user.ID)
	}
	if got.Email != user.Email {
		t.Errorf("Email: got %q, want %q", got.Email, user.Email)
	}
}

func TestHTTPClient_GetUser_ClassicPAT(t *testing.T) {
	user := User{Login: "octocat", ID: 1, Email: "octocat@github.com"}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-OAuth-Scopes", "repo, user")
		json.NewEncoder(w).Encode(user)
	}))
	defer srv.Close()

	client := NewHTTPClient(WithBaseURL(srv.URL))
	got, isClassic, err := client.GetUser(context.Background(), testToken)
	if err != nil {
		t.Fatalf("GetUser returned error: %v", err)
	}
	if !isClassic {
		t.Error("expected isClassicPAT=true, got false")
	}
	if got.Login != user.Login {
		t.Errorf("Login: got %q, want %q", got.Login, user.Login)
	}
}

func TestHTTPClient_GetUser_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials"}`)
	}))
	defer srv.Close()

	client := NewHTTPClient(WithBaseURL(srv.URL))
	_, _, err := client.GetUser(context.Background(), testToken)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestHTTPClient_GetUser_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message":"Internal Server Error"}`)
	}))
	defer srv.Close()

	client := NewHTTPClient(WithBaseURL(srv.URL))
	_, _, err := client.GetUser(context.Background(), testToken)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrUnauthorized) {
		t.Error("should not be ErrUnauthorized")
	}
	// Verify the error contains the status code.
	want := "500"
	if got := err.Error(); !contains(got, want) {
		t.Errorf("error %q should contain %q", got, want)
	}
}

func TestHTTPClient_CheckOrgMembership_IsMember(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orgs/my-org/members/octocat" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := NewHTTPClient(WithBaseURL(srv.URL))
	err := client.CheckOrgMembership(context.Background(), testToken, "my-org", "octocat")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestHTTPClient_CheckOrgMembership_NotMember(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	}))
	defer srv.Close()

	client := NewHTTPClient(WithBaseURL(srv.URL))
	err := client.CheckOrgMembership(context.Background(), testToken, "my-org", "octocat")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrNotOrgMember) {
		t.Errorf("expected ErrNotOrgMember, got: %v", err)
	}
}

func TestHTTPClient_CheckOrgMembership_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials"}`)
	}))
	defer srv.Close()

	client := NewHTTPClient(WithBaseURL(srv.URL))
	err := client.CheckOrgMembership(context.Background(), testToken, "my-org", "octocat")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestHTTPClient_ListUserTeams_Success(t *testing.T) {
	teams := []Team{
		{Slug: "backend", Organization: Organization{Login: "my-org"}},
		{Slug: "frontend", Organization: Organization{Login: "my-org"}},
		{Slug: "infra", Organization: Organization{Login: "other-org"}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/teams" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(teams)
	}))
	defer srv.Close()

	client := NewHTTPClient(WithBaseURL(srv.URL))
	got, err := client.ListUserTeams(context.Background(), testToken, "my-org")
	if err != nil {
		t.Fatalf("ListUserTeams returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 teams, got %d", len(got))
	}
	for _, team := range got {
		if team.Organization.Login != "my-org" {
			t.Errorf("unexpected org: %s", team.Organization.Login)
		}
	}
	if got[0].Slug != "backend" {
		t.Errorf("expected first team slug 'backend', got %q", got[0].Slug)
	}
	if got[1].Slug != "frontend" {
		t.Errorf("expected second team slug 'frontend', got %q", got[1].Slug)
	}
}

func TestHTTPClient_ListUserTeams_Pagination(t *testing.T) {
	page1Teams := []Team{
		{Slug: "backend", Organization: Organization{Login: "my-org"}},
		{Slug: "infra", Organization: Organization{Login: "other-org"}},
	}
	page2Teams := []Team{
		{Slug: "frontend", Organization: Organization{Login: "my-org"}},
		{Slug: "devops", Organization: Organization{Login: "my-org"}},
	}

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("page") == "2" {
			// Second page, no Link header.
			json.NewEncoder(w).Encode(page2Teams)
			return
		}

		// First page with Link header pointing to page 2.
		nextURL := fmt.Sprintf("http://%s/user/teams?per_page=100&page=2", r.Host)
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, nextURL))
		json.NewEncoder(w).Encode(page1Teams)
	}))
	defer srv.Close()

	client := NewHTTPClient(WithBaseURL(srv.URL))
	got, err := client.ListUserTeams(context.Background(), testToken, "my-org")
	if err != nil {
		t.Fatalf("ListUserTeams returned error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 HTTP calls (pagination), got %d", callCount)
	}
	// Should have 3 teams from my-org: backend (page1), frontend (page2), devops (page2).
	if len(got) != 3 {
		t.Fatalf("expected 3 filtered teams, got %d", len(got))
	}
	slugs := make(map[string]bool)
	for _, team := range got {
		slugs[team.Slug] = true
		if team.Organization.Login != "my-org" {
			t.Errorf("unexpected org: %s", team.Organization.Login)
		}
	}
	for _, expected := range []string{"backend", "frontend", "devops"} {
		if !slugs[expected] {
			t.Errorf("missing expected team %q", expected)
		}
	}
}

func TestHTTPClient_ListUserTeams_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()

	client := NewHTTPClient(WithBaseURL(srv.URL))
	got, err := client.ListUserTeams(context.Background(), testToken, "my-org")
	if err != nil {
		t.Fatalf("ListUserTeams returned error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 teams, got %d", len(got))
	}
}

func TestHTTPClient_ImplementsInterface(t *testing.T) {
	// This is a compile-time check via the package-level var above.
	// This test exists for documentation and to satisfy the test list requirement.
	var c Client = NewHTTPClient()
	if c == nil {
		t.Fatal("NewHTTPClient returned nil")
	}
}

func TestHTTPClient_AuthHeaderSet(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(User{Login: "test", ID: 1})
	}))
	defer srv.Close()

	client := NewHTTPClient(WithBaseURL(srv.URL))
	_, _, err := client.GetUser(context.Background(), testToken)
	if err != nil {
		t.Fatalf("GetUser returned error: %v", err)
	}
	want := "Bearer " + testToken
	if gotAuth != want {
		t.Errorf("Authorization header: got %q, want %q", gotAuth, want)
	}
}

// TestHTTPClient_ListUserTeams_CaseInsensitiveFilter verifies that org
// filtering is case-insensitive.
func TestHTTPClient_ListUserTeams_CaseInsensitiveFilter(t *testing.T) {
	teams := []Team{
		{Slug: "backend", Organization: Organization{Login: "My-Org"}},
		{Slug: "frontend", Organization: Organization{Login: "MY-ORG"}},
		{Slug: "infra", Organization: Organization{Login: "other-org"}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(teams)
	}))
	defer srv.Close()

	client := NewHTTPClient(WithBaseURL(srv.URL))
	got, err := client.ListUserTeams(context.Background(), testToken, "my-org")
	if err != nil {
		t.Fatalf("ListUserTeams returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 teams, got %d", len(got))
	}
}

func TestHTTPClient_GetUser_RateLimited_429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
	}))
	defer srv.Close()

	client := NewHTTPClient(WithBaseURL(srv.URL))
	_, _, err := client.GetUser(context.Background(), testToken)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("expected ErrRateLimited, got: %v", err)
	}
}

func TestHTTPClient_GetUser_RateLimited_Header(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1234567890")
		w.Header().Set("Content-Type", "application/json")
		// GitHub returns 403 when rate limited via header.
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
	}))
	defer srv.Close()

	client := NewHTTPClient(WithBaseURL(srv.URL))
	_, _, err := client.GetUser(context.Background(), testToken)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("expected ErrRateLimited, got: %v", err)
	}
}

func TestHTTPClient_CheckOrgMembership_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	client := NewHTTPClient(WithBaseURL(srv.URL))
	err := client.CheckOrgMembership(context.Background(), testToken, "my-org", "octocat")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("expected ErrRateLimited, got: %v", err)
	}
}

// contains reports whether s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
