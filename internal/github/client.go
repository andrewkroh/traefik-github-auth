// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package github

import (
	"context"
	"errors"
)

// Sentinel errors for GitHub API operations.
var (
	ErrUnauthorized = errors.New("github: unauthorized (invalid or revoked token)")
	ErrNotOrgMember = errors.New("github: user is not a member of the organization")
	ErrRateLimited  = errors.New("github: API rate limit exceeded")
)

// Client defines the interface for interacting with the GitHub API.
type Client interface {
	// GetUser retrieves the authenticated user's profile.
	// Returns the user and whether the response included X-OAuth-Scopes header
	// (which indicates a classic PAT rather than a fine-grained PAT).
	GetUser(ctx context.Context, token string) (*User, bool, error)

	// CheckOrgMembership checks if the user is a member of the given org.
	// Returns nil if the user is a member (HTTP 204), ErrNotOrgMember if not (HTTP 404).
	CheckOrgMembership(ctx context.Context, token, org, username string) error

	// ListUserTeams lists teams for the authenticated user, filtered to the given org.
	ListUserTeams(ctx context.Context, token, org string) ([]Team, error)
}
