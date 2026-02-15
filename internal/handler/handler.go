// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

// Package handler provides HTTP handlers for the ForwardAuth service.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/andrewkroh/traefik-github-auth/internal/validator"
)

// TokenValidator defines the interface for token validation.
// This allows the handler to be tested with a mock validator.
type TokenValidator interface {
	Validate(ctx context.Context, token string) (*validator.ValidationResult, error)
}

// Handler provides HTTP handlers for the ForwardAuth service.
type Handler struct {
	validator TokenValidator
	log       *slog.Logger
}

// New creates a new Handler with the given validator and logger.
func New(v TokenValidator, log *slog.Logger) *Handler {
	return &Handler{
		validator: v,
		log:       log,
	}
}

// Routes returns an http.Handler with all routes registered.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/validate", h.handleValidate)
	mux.HandleFunc("GET /healthz", h.handleHealthz)
	mux.HandleFunc("GET /ready", h.handleReady)
	return mux
}

// getSourceIP extracts the client IP address from the request.
// It first checks the X-Forwarded-For header (used when behind a proxy).
// If X-Forwarded-For contains multiple IPs, it returns the leftmost (original client).
// Otherwise, it falls back to RemoteAddr.
func getSourceIP(r *http.Request) string {
	// Check X-Forwarded-For header first.
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		// X-Forwarded-For can contain multiple IPs: "client, proxy1, proxy2"
		// The leftmost is the original client.
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			clientIP := strings.TrimSpace(ips[0])
			if clientIP != "" {
				return clientIP
			}
		}
	}

	// Fall back to RemoteAddr.
	// RemoteAddr is in the format "IP:port", so we need to strip the port.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// If SplitHostPort fails, return RemoteAddr as-is.
		return r.RemoteAddr
	}
	return host
}

// authHeaderPrefix is the prefix for all headers set by this service.
// Incoming requests must not contain these headers to prevent injection attacks.
const authHeaderPrefix = "X-Auth-User-"

// handleValidate is the ForwardAuth handler that validates GitHub PATs.
func (h *Handler) handleValidate(w http.ResponseWriter, r *http.Request) {
	sourceIP := getSourceIP(r)

	// Reject requests with pre-set auth identity headers to prevent
	// header injection attacks (spoofing user identity).
	for name := range r.Header {
		if strings.HasPrefix(name, authHeaderPrefix) {
			h.log.WarnContext(r.Context(), "Request contains injected auth header",
				slog.String("header", name),
				slog.String("source.ip", sourceIP),
			)
			writeJSONError(w, http.StatusForbidden, "forbidden: request contains disallowed headers")
			return
		}
	}

	// Extract the Authorization header.
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		h.log.WarnContext(r.Context(), "Missing Authorization header",
			slog.String("source.ip", sourceIP),
		)
		writeJSONError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
		return
	}

	// Parse "Bearer <token>".
	token, ok := parseBearerToken(authHeader)
	if !ok {
		h.log.WarnContext(r.Context(), "Malformed Authorization header",
			slog.String("source.ip", sourceIP),
		)
		writeJSONError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
		return
	}

	// Validate the token.
	result, err := h.validator.Validate(r.Context(), token)
	if err != nil {
		h.handleValidationError(r.Context(), w, sourceIP, err)
		return
	}

	// Success: set response headers with user info.
	w.Header().Set("X-Auth-User-Login", result.Login)
	w.Header().Set("X-Auth-User-Id", fmt.Sprintf("%d", result.ID))
	w.Header().Set("X-Auth-User-Org", result.Org)
	w.Header().Set("X-Auth-User-Teams", strings.Join(result.Teams, ","))

	h.log.InfoContext(r.Context(), "Authentication successful",
		slog.String("login", result.Login),
		slog.Int64("user_id", result.ID),
		slog.String("source.ip", sourceIP),
	)

	w.WriteHeader(http.StatusOK)
}

// handleValidationError maps validation errors to appropriate HTTP responses.
func (h *Handler) handleValidationError(ctx context.Context, w http.ResponseWriter, sourceIP string, err error) {
	switch {
	case errors.Is(err, validator.ErrUnauthorized):
		h.log.WarnContext(ctx, "Token validation failed: unauthorized",
			slog.String("source.ip", sourceIP),
		)
		writeJSONError(w, http.StatusUnauthorized, "access denied")
	case errors.Is(err, validator.ErrNotOrgMember):
		h.log.WarnContext(ctx, "Token validation failed: not an org member",
			slog.String("source.ip", sourceIP),
		)
		writeJSONError(w, http.StatusForbidden, "access denied")
	case errors.Is(err, validator.ErrClassicPAT):
		h.log.WarnContext(ctx, "Token validation failed: classic PAT rejected",
			slog.String("source.ip", sourceIP),
		)
		writeJSONError(w, http.StatusForbidden, "forbidden: classic PATs are not allowed")
	case errors.Is(err, validator.ErrRateLimited):
		h.log.WarnContext(ctx, "Token validation failed: rate limited",
			slog.String("source.ip", sourceIP),
		)
		writeJSONError(w, http.StatusTooManyRequests, "rate limit exceeded, try again later")
	default:
		h.log.ErrorContext(ctx, "Token validation failed: internal error",
			slog.String("error", err.Error()),
			slog.String("source.ip", sourceIP),
		)
		writeJSONError(w, http.StatusInternalServerError, "internal server error")
	}
}

// handleHealthz responds with a simple health check.
func (h *Handler) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

// handleReady responds with a simple readiness check.
func (h *Handler) handleReady(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

// parseBearerToken extracts the token from a "Bearer <token>" Authorization header.
// Returns the token and true if valid, or empty string and false if malformed.
func parseBearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimPrefix(header, prefix)
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	return token, true
}

// errorResponse is the JSON structure for error responses.
type errorResponse struct {
	Error string `json:"error"`
}

// writeJSONError writes a JSON error response with the given status code and message.
func writeJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(errorResponse{Error: message})
}
