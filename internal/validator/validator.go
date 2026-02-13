// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

// Package validator provides token validation orchestration.
package validator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/andrewkroh/traefik-github-auth/internal/github"
)

// Sentinel errors returned by the Validator.
var (
	ErrUnauthorized = errors.New("unauthorized: invalid or revoked token")
	ErrNotOrgMember = errors.New("forbidden: user is not a member of the organization")
	ErrClassicPAT   = errors.New("forbidden: classic PATs are not allowed, use a fine-grained PAT")
	ErrRateLimited  = errors.New("rate limited: GitHub API rate limit exceeded")
)

// Auth result attribute values used for OTel metrics and spans.
const (
	resultSuccess      = "success"
	resultUnauthorized = "unauthorized"
	resultForbidden    = "forbidden"
	resultError        = "error"
)

// ValidationResult holds the outcome of a successful token validation.
type ValidationResult struct {
	// Login is the GitHub username.
	Login string

	// ID is the GitHub user ID.
	ID int64

	// Email is the GitHub user's email address.
	Email string

	// Org is the GitHub organization that was validated.
	Org string

	// Teams contains the team slugs within the configured organization
	// that the user belongs to.
	Teams []string
}

// Cache defines the interface for caching validation results.
// The validator uses this interface to avoid repeated GitHub API calls
// for the same token within the cache TTL.
type Cache interface {
	// Get retrieves a cached entry for the given token.
	// Returns the result, an optional error (for negative cache entries),
	// and whether the entry was found.
	//
	// Positive hit: (result, nil, true)
	// Negative hit: (zero, err, true)
	// Miss:         (zero, nil, false)
	Get(token string) (ValidationResult, error, bool)

	// Set stores a validation result for the given token.
	// Pass a non-nil err to cache a negative result (e.g., unauthorized).
	Set(token string, result ValidationResult, err error)

	// Delete removes a cached entry for the given token.
	Delete(token string)
}

// Validator orchestrates token validation by checking the cache and
// calling the GitHub API as needed.
type Validator struct {
	github            github.Client
	cache             Cache
	org               string
	rejectClassicPATs bool
	log               *slog.Logger

	tracer          trace.Tracer
	validationTotal metric.Int64Counter
}

// New creates a new Validator with the given dependencies.
func New(ghClient github.Client, cache Cache, org string, rejectClassicPATs bool, log *slog.Logger) *Validator {
	tracer := otel.Tracer("github.com/andrewkroh/traefik-github-auth/internal/validator")
	meter := otel.Meter("github.com/andrewkroh/traefik-github-auth/internal/validator")

	validationTotal, _ := meter.Int64Counter("github_auth.validation.total",
		metric.WithDescription("Total number of token validations"),
	)

	return &Validator{
		github:            ghClient,
		cache:             cache,
		org:               org,
		rejectClassicPATs: rejectClassicPATs,
		log:               log,
		tracer:            tracer,
		validationTotal:   validationTotal,
	}
}

// Validate checks whether the given token is valid and the user is
// authorized. It follows a 3-step validation flow:
//  1. Identify the user via GetUser.
//  2. Verify organization membership via CheckOrgMembership.
//  3. List the user's teams via ListUserTeams.
//
// Results are cached to avoid redundant API calls.
func (v *Validator) Validate(ctx context.Context, token string) (*ValidationResult, error) {
	ctx, span := v.tracer.Start(ctx, "validate_token")
	defer span.End()

	// Check cache first.
	if result, cachedErr, ok := v.cache.Get(token); ok {
		span.SetAttributes(attribute.Bool("cache.hit", true))

		// Negative cache hit (e.g., previously unauthorized token).
		if cachedErr != nil {
			span.RecordError(cachedErr)
			span.SetStatus(codes.Error, cachedErr.Error())
			span.SetAttributes(attribute.String("auth.result", resultUnauthorized))
			v.validationTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("result", resultUnauthorized)))

			v.log.DebugContext(ctx, "Negative cache hit",
				slog.String("error", cachedErr.Error()),
			)

			return nil, cachedErr
		}

		// Positive cache hit.
		span.SetAttributes(attribute.String("auth.user.login", result.Login))
		span.SetAttributes(attribute.String("auth.result", resultSuccess))
		v.validationTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("result", resultSuccess)))

		v.log.DebugContext(ctx, "Cache hit for token validation",
			slog.String("login", result.Login),
		)

		return &result, nil
	}

	span.SetAttributes(attribute.Bool("cache.hit", false))

	// Step 1: Identify the user.
	user, isClassicPAT, err := v.github.GetUser(ctx, token)
	if err != nil {
		if errors.Is(err, github.ErrRateLimited) {
			span.RecordError(ErrRateLimited)
			span.SetStatus(codes.Error, ErrRateLimited.Error())
			span.SetAttributes(attribute.String("auth.result", resultError))
			v.validationTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("result", resultError)))
			v.log.WarnContext(ctx, "Token validation failed: rate limited")
			return nil, fmt.Errorf("%w", ErrRateLimited)
		}

		if errors.Is(err, github.ErrUnauthorized) {
			v.cache.Set(token, ValidationResult{}, ErrUnauthorized)

			span.RecordError(ErrUnauthorized)
			span.SetStatus(codes.Error, ErrUnauthorized.Error())
			span.SetAttributes(attribute.String("auth.result", resultUnauthorized))
			v.validationTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("result", resultUnauthorized)))

			v.log.WarnContext(ctx, "Token validation failed: unauthorized")

			return nil, fmt.Errorf("%w", ErrUnauthorized)
		}

		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.String("auth.result", resultError))
		v.validationTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("result", resultError)))

		v.log.ErrorContext(ctx, "Failed to get user from GitHub", slog.String("error", err.Error()))

		return nil, fmt.Errorf("getting user: %w", err)
	}

	// Check for classic PAT rejection.
	if v.rejectClassicPATs && isClassicPAT {
		span.RecordError(ErrClassicPAT)
		span.SetStatus(codes.Error, ErrClassicPAT.Error())
		span.SetAttributes(attribute.String("auth.result", resultForbidden))
		v.validationTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("result", resultForbidden)))

		v.log.WarnContext(ctx, "Token validation failed: classic PAT rejected",
			slog.String("login", user.Login),
		)

		return nil, fmt.Errorf("%w", ErrClassicPAT)
	}

	// Step 2: Verify organization membership.
	if err := v.github.CheckOrgMembership(ctx, token, v.org, user.Login); err != nil {
		if errors.Is(err, github.ErrRateLimited) {
			span.RecordError(ErrRateLimited)
			span.SetStatus(codes.Error, ErrRateLimited.Error())
			span.SetAttributes(attribute.String("auth.result", resultError))
			v.validationTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("result", resultError)))
			v.log.WarnContext(ctx, "Token validation failed: rate limited")
			return nil, fmt.Errorf("%w", ErrRateLimited)
		}

		if errors.Is(err, github.ErrNotOrgMember) {
			span.RecordError(ErrNotOrgMember)
			span.SetStatus(codes.Error, ErrNotOrgMember.Error())
			span.SetAttributes(attribute.String("auth.result", resultForbidden))
			v.validationTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("result", resultForbidden)))

			v.log.WarnContext(ctx, "Token validation failed: user is not an org member",
				slog.String("login", user.Login),
				slog.String("org", v.org),
			)

			return nil, fmt.Errorf("%w", ErrNotOrgMember)
		}

		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.String("auth.result", resultError))
		v.validationTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("result", resultError)))

		v.log.ErrorContext(ctx, "Failed to check org membership",
			slog.String("login", user.Login),
			slog.String("org", v.org),
			slog.String("error", err.Error()),
		)

		return nil, fmt.Errorf("checking org membership: %w", err)
	}

	// Step 3: Get teams.
	teams, err := v.github.ListUserTeams(ctx, token, v.org)
	if err != nil {
		if errors.Is(err, github.ErrRateLimited) {
			span.RecordError(ErrRateLimited)
			span.SetStatus(codes.Error, ErrRateLimited.Error())
			span.SetAttributes(attribute.String("auth.result", resultError))
			v.validationTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("result", resultError)))
			v.log.WarnContext(ctx, "Token validation failed: rate limited")
			return nil, fmt.Errorf("%w", ErrRateLimited)
		}

		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.String("auth.result", resultError))
		v.validationTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("result", resultError)))

		v.log.ErrorContext(ctx, "Failed to list user teams",
			slog.String("login", user.Login),
			slog.String("org", v.org),
			slog.String("error", err.Error()),
		)

		return nil, fmt.Errorf("listing user teams: %w", err)
	}

	// Extract team slugs.
	teamSlugs := make([]string, len(teams))
	for i, t := range teams {
		teamSlugs[i] = t.Slug
	}

	// Build result.
	result := ValidationResult{
		Login: user.Login,
		ID:    user.ID,
		Email: user.Email,
		Org:   v.org,
		Teams: teamSlugs,
	}

	// Cache the result.
	v.cache.Set(token, result, nil)

	span.SetAttributes(attribute.String("auth.user.login", user.Login))
	span.SetAttributes(attribute.String("auth.result", resultSuccess))
	v.validationTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("result", resultSuccess)))

	v.log.InfoContext(ctx, "Token validation succeeded",
		slog.String("login", user.Login),
		slog.Int64("user_id", user.ID),
		slog.Int("teams", len(teamSlugs)),
	)

	return &result, nil
}
