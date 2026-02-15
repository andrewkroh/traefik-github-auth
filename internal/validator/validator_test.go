// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package validator

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/andrewkroh/traefik-github-auth/internal/github"
)

// mockGitHubClient implements github.Client for testing.
type mockGitHubClient struct {
	getUser            func(ctx context.Context, token string) (*github.User, bool, error)
	checkOrgMembership func(ctx context.Context, token, org, username string) error
	listUserTeams      func(ctx context.Context, token, org string) ([]github.Team, error)
}

func (m *mockGitHubClient) GetUser(ctx context.Context, token string) (*github.User, bool, error) {
	return m.getUser(ctx, token)
}

func (m *mockGitHubClient) CheckOrgMembership(ctx context.Context, token, org, username string) error {
	return m.checkOrgMembership(ctx, token, org, username)
}

func (m *mockGitHubClient) ListUserTeams(ctx context.Context, token, org string) ([]github.Team, error) {
	return m.listUserTeams(ctx, token, org)
}

// mockCacheEntry stores both a result and an optional error for negative caching.
type mockCacheEntry struct {
	result ValidationResult
	err    error
}

// mockCache implements Cache for testing.
type mockCache struct {
	store   map[string]mockCacheEntry
	deleted []string
}

func newMockCache() *mockCache {
	return &mockCache{
		store: make(map[string]mockCacheEntry),
	}
}

func (c *mockCache) Get(token string) (ValidationResult, error, bool) {
	entry, ok := c.store[token]
	if !ok {
		return ValidationResult{}, nil, false
	}
	return entry.result, entry.err, true
}

func (c *mockCache) Set(token string, result ValidationResult, err error) {
	c.store[token] = mockCacheEntry{result: result, err: err}
}

func (c *mockCache) Delete(token string) {
	c.deleted = append(c.deleted, token)
	delete(c.store, token)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(
		nopWriter{},
		&slog.HandlerOptions{Level: slog.LevelDebug},
	))
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestValidate_CacheHit(t *testing.T) {
	cache := newMockCache()
	cache.store["fake-token-cached"] = mockCacheEntry{
		result: ValidationResult{
			Login: "cacheduser",
			ID:    100,
			Org:   "test-org",
			Teams: []string{"team-alpha"},
		},
	}

	getUserCalled := false
	ghClient := &mockGitHubClient{
		getUser: func(ctx context.Context, token string) (*github.User, bool, error) {
			getUserCalled = true
			return nil, false, errors.New("should not be called")
		},
	}

	v := New(ghClient, cache, "myorg", false, discardLogger())
	result, err := v.Validate(context.Background(), "fake-token-cached")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if getUserCalled {
		t.Fatal("expected GitHub API not to be called on cache hit")
	}
	if result.Login != "cacheduser" {
		t.Errorf("expected login 'cacheduser', got %q", result.Login)
	}
	if result.ID != 100 {
		t.Errorf("expected ID 100, got %d", result.ID)
	}
	if len(result.Teams) != 1 || result.Teams[0] != "team-alpha" {
		t.Errorf("expected teams [team-alpha], got %v", result.Teams)
	}
}

func TestValidate_NegativeCacheHit(t *testing.T) {
	cache := newMockCache()
	cache.store["fake-token-bad"] = mockCacheEntry{
		err: ErrUnauthorized,
	}

	getUserCalled := false
	ghClient := &mockGitHubClient{
		getUser: func(ctx context.Context, token string) (*github.User, bool, error) {
			getUserCalled = true
			return nil, false, errors.New("should not be called")
		},
	}

	v := New(ghClient, cache, "myorg", false, discardLogger())
	_, err := v.Validate(context.Background(), "fake-token-bad")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got: %v", err)
	}
	if getUserCalled {
		t.Fatal("expected GitHub API not to be called on negative cache hit")
	}
}

func TestValidate_CacheMiss_Success(t *testing.T) {
	cache := newMockCache()

	ghClient := &mockGitHubClient{
		getUser: func(ctx context.Context, token string) (*github.User, bool, error) {
			return &github.User{Login: "testuser", ID: 42}, false, nil
		},
		checkOrgMembership: func(ctx context.Context, token, org, username string) error {
			if org != "myorg" {
				t.Errorf("expected org 'myorg', got %q", org)
			}
			if username != "testuser" {
				t.Errorf("expected username 'testuser', got %q", username)
			}
			return nil
		},
		listUserTeams: func(ctx context.Context, token, org string) ([]github.Team, error) {
			return []github.Team{
				{Slug: "backend", Organization: github.Organization{Login: "myorg"}},
				{Slug: "frontend", Organization: github.Organization{Login: "myorg"}},
			}, nil
		},
	}

	v := New(ghClient, cache, "myorg", false, discardLogger())
	result, err := v.Validate(context.Background(), "fake-token-miss")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result.Login != "testuser" {
		t.Errorf("expected login 'testuser', got %q", result.Login)
	}
	if result.ID != 42 {
		t.Errorf("expected ID 42, got %d", result.ID)
	}
	if len(result.Teams) != 2 {
		t.Fatalf("expected 2 teams, got %d", len(result.Teams))
	}
	if result.Teams[0] != "backend" || result.Teams[1] != "frontend" {
		t.Errorf("expected teams [backend, frontend], got %v", result.Teams)
	}

	// Verify cache was populated.
	cached, ok := cache.store["fake-token-miss"]
	if !ok {
		t.Fatal("expected result to be cached")
	}
	if cached.err != nil {
		t.Errorf("expected nil error in cache entry, got: %v", cached.err)
	}
	if cached.result.Login != "testuser" {
		t.Errorf("expected cached login 'testuser', got %q", cached.result.Login)
	}
}

func TestValidate_UnauthorizedToken(t *testing.T) {
	cache := newMockCache()

	ghClient := &mockGitHubClient{
		getUser: func(ctx context.Context, token string) (*github.User, bool, error) {
			return nil, false, github.ErrUnauthorized
		},
	}

	v := New(ghClient, cache, "myorg", false, discardLogger())
	_, err := v.Validate(context.Background(), "fake-token-unauth")

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got: %v", err)
	}

	// Verify the unauthorized result was negatively cached.
	entry, ok := cache.store["fake-token-unauth"]
	if !ok {
		t.Fatal("expected unauthorized token to be negatively cached")
	}
	if !errors.Is(entry.err, ErrUnauthorized) {
		t.Errorf("expected cached error ErrUnauthorized, got: %v", entry.err)
	}
}

func TestValidate_NotOrgMember(t *testing.T) {
	cache := newMockCache()

	ghClient := &mockGitHubClient{
		getUser: func(ctx context.Context, token string) (*github.User, bool, error) {
			return &github.User{Login: "outsider", ID: 99}, false, nil
		},
		checkOrgMembership: func(ctx context.Context, token, org, username string) error {
			return github.ErrNotOrgMember
		},
	}

	v := New(ghClient, cache, "myorg", false, discardLogger())
	_, err := v.Validate(context.Background(), "fake-token-nonmember")

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrNotOrgMember) {
		t.Errorf("expected ErrNotOrgMember, got: %v", err)
	}
}

func TestValidate_ClassicPAT_Rejected(t *testing.T) {
	cache := newMockCache()

	ghClient := &mockGitHubClient{
		getUser: func(ctx context.Context, token string) (*github.User, bool, error) {
			return &github.User{Login: "classicuser", ID: 55}, true, nil
		},
	}

	v := New(ghClient, cache, "myorg", true, discardLogger())
	_, err := v.Validate(context.Background(), "fake-token-classic")

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrClassicPAT) {
		t.Errorf("expected ErrClassicPAT, got: %v", err)
	}
}

func TestValidate_ClassicPAT_Allowed(t *testing.T) {
	cache := newMockCache()

	ghClient := &mockGitHubClient{
		getUser: func(ctx context.Context, token string) (*github.User, bool, error) {
			return &github.User{Login: "classicuser", ID: 55}, true, nil
		},
		checkOrgMembership: func(ctx context.Context, token, org, username string) error {
			return nil
		},
		listUserTeams: func(ctx context.Context, token, org string) ([]github.Team, error) {
			return []github.Team{
				{Slug: "devs", Organization: github.Organization{Login: "myorg"}},
			}, nil
		},
	}

	v := New(ghClient, cache, "myorg", false, discardLogger())
	result, err := v.Validate(context.Background(), "fake-token-classic-allowed")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result.Login != "classicuser" {
		t.Errorf("expected login 'classicuser', got %q", result.Login)
	}
	if len(result.Teams) != 1 || result.Teams[0] != "devs" {
		t.Errorf("expected teams [devs], got %v", result.Teams)
	}
}

func TestValidate_GetUserError(t *testing.T) {
	cache := newMockCache()
	apiErr := errors.New("github API rate limit exceeded")

	ghClient := &mockGitHubClient{
		getUser: func(ctx context.Context, token string) (*github.User, bool, error) {
			return nil, false, apiErr
		},
	}

	v := New(ghClient, cache, "myorg", false, discardLogger())
	_, err := v.Validate(context.Background(), "fake-token-error")

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, apiErr) {
		t.Errorf("expected wrapped apiErr, got: %v", err)
	}
	// Should NOT match sentinel errors.
	if errors.Is(err, ErrUnauthorized) {
		t.Error("should not match ErrUnauthorized")
	}
}

func TestValidate_CheckOrgError(t *testing.T) {
	cache := newMockCache()
	apiErr := errors.New("github API network error")

	ghClient := &mockGitHubClient{
		getUser: func(ctx context.Context, token string) (*github.User, bool, error) {
			return &github.User{Login: "testuser", ID: 42}, false, nil
		},
		checkOrgMembership: func(ctx context.Context, token, org, username string) error {
			return apiErr
		},
	}

	v := New(ghClient, cache, "myorg", false, discardLogger())
	_, err := v.Validate(context.Background(), "fake-token-org-error")

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, apiErr) {
		t.Errorf("expected wrapped apiErr, got: %v", err)
	}
	// Should NOT match sentinel errors.
	if errors.Is(err, ErrNotOrgMember) {
		t.Error("should not match ErrNotOrgMember")
	}
}

func TestValidate_ListTeamsError(t *testing.T) {
	cache := newMockCache()
	apiErr := errors.New("github API timeout")

	ghClient := &mockGitHubClient{
		getUser: func(ctx context.Context, token string) (*github.User, bool, error) {
			return &github.User{Login: "testuser", ID: 42}, false, nil
		},
		checkOrgMembership: func(ctx context.Context, token, org, username string) error {
			return nil
		},
		listUserTeams: func(ctx context.Context, token, org string) ([]github.Team, error) {
			return nil, apiErr
		},
	}

	v := New(ghClient, cache, "myorg", false, discardLogger())
	_, err := v.Validate(context.Background(), "fake-token-teams-error")

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, apiErr) {
		t.Errorf("expected wrapped apiErr, got: %v", err)
	}
}

func TestValidate_TeamsExtracted(t *testing.T) {
	cache := newMockCache()

	ghClient := &mockGitHubClient{
		getUser: func(ctx context.Context, token string) (*github.User, bool, error) {
			return &github.User{Login: "teamuser", ID: 77}, false, nil
		},
		checkOrgMembership: func(ctx context.Context, token, org, username string) error {
			return nil
		},
		listUserTeams: func(ctx context.Context, token, org string) ([]github.Team, error) {
			return []github.Team{
				{Slug: "platform", Organization: github.Organization{Login: "myorg"}},
				{Slug: "security", Organization: github.Organization{Login: "myorg"}},
				{Slug: "sre", Organization: github.Organization{Login: "myorg"}},
			}, nil
		},
	}

	v := New(ghClient, cache, "myorg", false, discardLogger())
	result, err := v.Validate(context.Background(), "fake-token-teams")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	expectedTeams := []string{"platform", "security", "sre"}
	if len(result.Teams) != len(expectedTeams) {
		t.Fatalf("expected %d teams, got %d", len(expectedTeams), len(result.Teams))
	}
	for i, expected := range expectedTeams {
		if result.Teams[i] != expected {
			t.Errorf("team[%d]: expected %q, got %q", i, expected, result.Teams[i])
		}
	}

	if result.Login != "teamuser" {
		t.Errorf("expected login 'teamuser', got %q", result.Login)
	}
	if result.ID != 77 {
		t.Errorf("expected ID 77, got %d", result.ID)
	}
}
