// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	defaultBaseURL = "https://api.github.com"
	acceptHeader   = "application/vnd.github+json"
	tracerName     = "github.com/andrewkroh/traefik-github-auth/internal/github"
)

// linkNextRE matches the "next" relation in a Link header value.
var linkNextRE = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// HTTPClient is a concrete implementation of the Client interface that
// communicates with the GitHub API over HTTP.
type HTTPClient struct {
	httpClient *http.Client
	baseURL    string
	log        *slog.Logger
}

// Option configures an HTTPClient.
type Option func(*HTTPClient)

// WithBaseURL sets the base URL for the GitHub API.
func WithBaseURL(url string) Option {
	return func(c *HTTPClient) {
		c.baseURL = strings.TrimRight(url, "/")
	}
}

// WithHTTPClient sets the underlying HTTP client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *HTTPClient) {
		c.httpClient = hc
	}
}

// WithLogger sets the structured logger.
func WithLogger(l *slog.Logger) Option {
	return func(c *HTTPClient) {
		c.log = l
	}
}

// NewHTTPClient creates a new HTTPClient with the given options.
// By default it uses https://api.github.com as the base URL,
// http.DefaultClient, and slog.Default() as the logger.
func NewHTTPClient(opts ...Option) *HTTPClient {
	c := &HTTPClient{
		httpClient: http.DefaultClient,
		baseURL:    defaultBaseURL,
		log:        slog.Default(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// tracer returns the OTel tracer for this package.
func (c *HTTPClient) tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// newRequest creates an authenticated GitHub API request.
func (c *HTTPClient) newRequest(ctx context.Context, method, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	return req, nil
}

// setHeaders sets the standard GitHub API headers on a request.
func setHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", acceptHeader)
}

// checkRateLimit inspects the response for GitHub rate limit exhaustion.
// Returns ErrRateLimited if HTTP 429 or X-RateLimit-Remaining is "0".
func checkRateLimit(resp *http.Response) error {
	if resp.StatusCode == http.StatusTooManyRequests {
		return ErrRateLimited
	}
	remaining := resp.Header.Get("X-RateLimit-Remaining")
	if remaining == "" {
		return nil
	}
	n, err := strconv.Atoi(remaining)
	if err != nil {
		return nil
	}
	if n == 0 {
		return ErrRateLimited
	}
	return nil
}

// GetUser retrieves the authenticated user's profile.
// Returns the user and whether the response included X-OAuth-Scopes header
// (which indicates a classic PAT rather than a fine-grained PAT).
func (c *HTTPClient) GetUser(ctx context.Context, token string) (*User, bool, error) {
	ctx, span := c.tracer().Start(ctx, "github.get_user")
	defer span.End()

	urlPath := "/user"
	fullURL := c.baseURL + urlPath

	span.SetAttributes(
		attribute.String("http.request.method", "GET"),
		attribute.String("url.path", urlPath),
	)

	req, err := c.newRequest(ctx, http.MethodGet, fullURL)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		c.log.ErrorContext(ctx, "failed to create request", slog.String("method", "GetUser"), slog.String("error", err.Error()))
		return nil, false, fmt.Errorf("github: creating request: %w", err)
	}
	setHeaders(req, token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		c.log.ErrorContext(ctx, "request failed", slog.String("method", "GetUser"), slog.String("error", err.Error()))
		return nil, false, fmt.Errorf("github: executing request: %w", err)
	}
	defer resp.Body.Close()

	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))

	// Check for rate limiting before other status checks.
	if err := checkRateLimit(resp); err != nil {
		c.log.WarnContext(ctx, "rate limited by GitHub API", slog.String("method", "GetUser"))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, false, err
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		c.log.WarnContext(ctx, "unauthorized token", slog.String("method", "GetUser"))
		span.RecordError(ErrUnauthorized)
		span.SetStatus(codes.Error, ErrUnauthorized.Error())
		return nil, false, ErrUnauthorized

	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		body, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("github: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		c.log.ErrorContext(ctx, "unexpected response", slog.String("method", "GetUser"), slog.Int("status", resp.StatusCode))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, false, err
	}

	var user User
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		c.log.ErrorContext(ctx, "failed to decode response", slog.String("method", "GetUser"), slog.String("error", err.Error()))
		return nil, false, fmt.Errorf("github: decoding user response: %w", err)
	}

	// X-OAuth-Scopes is present for classic PATs but absent for fine-grained PATs.
	isClassicPAT := resp.Header.Get("X-OAuth-Scopes") != ""

	c.log.InfoContext(ctx, "fetched user", slog.String("login", user.Login), slog.Int64("id", user.ID), slog.Bool("is_classic_pat", isClassicPAT))
	return &user, isClassicPAT, nil
}

// CheckOrgMembership checks if the user is a member of the given org.
// Returns nil if the user is a member (HTTP 204), ErrNotOrgMember if not (HTTP 404).
func (c *HTTPClient) CheckOrgMembership(ctx context.Context, token, org, username string) error {
	ctx, span := c.tracer().Start(ctx, "github.check_org_membership")
	defer span.End()

	urlPath := fmt.Sprintf("/orgs/%s/members/%s", org, username)
	fullURL := c.baseURL + urlPath

	span.SetAttributes(
		attribute.String("http.request.method", "GET"),
		attribute.String("url.path", urlPath),
	)

	req, err := c.newRequest(ctx, http.MethodGet, fullURL)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		c.log.ErrorContext(ctx, "failed to create request", slog.String("method", "CheckOrgMembership"), slog.String("error", err.Error()))
		return fmt.Errorf("github: creating request: %w", err)
	}
	setHeaders(req, token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		c.log.ErrorContext(ctx, "request failed", slog.String("method", "CheckOrgMembership"), slog.String("error", err.Error()))
		return fmt.Errorf("github: executing request: %w", err)
	}
	defer resp.Body.Close()

	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))

	// Check for rate limiting before other status checks.
	if err := checkRateLimit(resp); err != nil {
		c.log.WarnContext(ctx, "rate limited by GitHub API", slog.String("method", "CheckOrgMembership"))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	switch resp.StatusCode {
	case http.StatusNoContent:
		c.log.InfoContext(ctx, "user is org member", slog.String("org", org), slog.String("username", username))
		return nil

	case http.StatusNotFound:
		c.log.WarnContext(ctx, "user is not org member", slog.String("org", org), slog.String("username", username))
		span.RecordError(ErrNotOrgMember)
		span.SetStatus(codes.Error, ErrNotOrgMember.Error())
		return ErrNotOrgMember

	case http.StatusUnauthorized:
		c.log.WarnContext(ctx, "unauthorized token", slog.String("method", "CheckOrgMembership"))
		span.RecordError(ErrUnauthorized)
		span.SetStatus(codes.Error, ErrUnauthorized.Error())
		return ErrUnauthorized

	default:
		body, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("github: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		c.log.ErrorContext(ctx, "unexpected response", slog.String("method", "CheckOrgMembership"), slog.Int("status", resp.StatusCode))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
}

// ListUserTeams lists teams for the authenticated user, filtered to the given org.
func (c *HTTPClient) ListUserTeams(ctx context.Context, token, org string) ([]Team, error) {
	ctx, span := c.tracer().Start(ctx, "github.list_user_teams")
	defer span.End()

	urlPath := "/user/teams"

	span.SetAttributes(
		attribute.String("http.request.method", "GET"),
		attribute.String("url.path", urlPath),
	)

	var allTeams []Team
	nextURL := c.baseURL + urlPath + "?per_page=100"

	for nextURL != "" {
		teams, next, err := c.fetchTeamsPage(ctx, token, nextURL)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		allTeams = append(allTeams, teams...)
		nextURL = next
	}

	// Filter to only teams in the specified org (case-insensitive).
	filtered := make([]Team, 0, len(allTeams))
	for _, t := range allTeams {
		if strings.EqualFold(t.Organization.Login, org) {
			filtered = append(filtered, t)
		}
	}

	c.log.InfoContext(ctx, "listed user teams",
		slog.String("org", org),
		slog.Int("total_teams", len(allTeams)),
		slog.Int("filtered_teams", len(filtered)),
	)

	return filtered, nil
}

// fetchTeamsPage fetches a single page of teams from the given URL.
// It returns the teams, the URL for the next page (or "" if none), and any error.
func (c *HTTPClient) fetchTeamsPage(ctx context.Context, token, url string) ([]Team, string, error) {
	req, err := c.newRequest(ctx, http.MethodGet, url)
	if err != nil {
		c.log.ErrorContext(ctx, "failed to create request", slog.String("method", "ListUserTeams"), slog.String("error", err.Error()))
		return nil, "", fmt.Errorf("github: creating request: %w", err)
	}
	setHeaders(req, token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.log.ErrorContext(ctx, "request failed", slog.String("method", "ListUserTeams"), slog.String("error", err.Error()))
		return nil, "", fmt.Errorf("github: executing request: %w", err)
	}
	defer resp.Body.Close()

	// Check for rate limiting before other status checks.
	if err := checkRateLimit(resp); err != nil {
		c.log.WarnContext(ctx, "rate limited by GitHub API", slog.String("method", "ListUserTeams"))
		return nil, "", err
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		c.log.WarnContext(ctx, "unauthorized token", slog.String("method", "ListUserTeams"))
		return nil, "", ErrUnauthorized

	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		body, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("github: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		c.log.ErrorContext(ctx, "unexpected response", slog.String("method", "ListUserTeams"), slog.Int("status", resp.StatusCode))
		return nil, "", err
	}

	var teams []Team
	if err := json.NewDecoder(resp.Body).Decode(&teams); err != nil {
		c.log.ErrorContext(ctx, "failed to decode response", slog.String("method", "ListUserTeams"), slog.String("error", err.Error()))
		return nil, "", fmt.Errorf("github: decoding teams response: %w", err)
	}

	// Parse Link header for pagination.
	nextURL := parseLinkNext(resp.Header.Get("Link"))

	return teams, nextURL, nil
}

// parseLinkNext extracts the URL for the "next" relation from a Link header.
// Returns "" if no "next" relation is found.
func parseLinkNext(header string) string {
	if header == "" {
		return ""
	}
	matches := linkNextRE.FindStringSubmatch(header)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}
