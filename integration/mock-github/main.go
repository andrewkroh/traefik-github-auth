// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

// Package main implements a mock GitHub API server for integration testing.
// It provides fake implementations of the GitHub API endpoints used by the
// token validator, allowing end-to-end testing without real GitHub credentials.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// userFixture holds the test data for a single mock user.
type userFixture struct {
	Login       string
	ID          int64
	Email       string
	IsOrgMember bool
	Teams       []string
	IsClassic   bool
}

// fixtures maps Bearer tokens to user data.
var fixtures = map[string]userFixture{
	"valid-test-token-1": {
		Login:       "testuser1",
		ID:          1001,
		Email:       "test1@example.com",
		IsOrgMember: true,
		Teams:       []string{"platform-eng", "backend"},
		IsClassic:   false,
	},
	"valid-test-token-2": {
		Login:       "testuser2",
		ID:          1002,
		Email:       "test2@example.com",
		IsOrgMember: true,
		Teams:       []string{"frontend"},
		IsClassic:   false,
	},
	"non-member-token": {
		Login:       "outsider",
		ID:          2001,
		Email:       "outsider@example.com",
		IsOrgMember: false,
		Teams:       nil,
		IsClassic:   false,
	},
	"classic-pat-token": {
		Login:       "classicuser",
		ID:          3001,
		Email:       "classic@example.com",
		IsOrgMember: true,
		Teams:       []string{"platform-eng"},
		IsClassic:   true,
	},
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /user", handleGetUser)
	mux.HandleFunc("GET /user/teams", handleListUserTeams)
	mux.HandleFunc("GET /orgs/{org}/members/{username}", handleCheckOrgMembership)

	log.Println("mock-github listening on :9090")
	if err := http.ListenAndServe(":9090", mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// extractToken parses the Bearer token from the Authorization header.
// Returns the token and true if valid, or empty string and false otherwise.
func extractToken(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return "", false
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	return token, true
}

// handleGetUser implements GET /user.
func handleGetUser(w http.ResponseWriter, r *http.Request) {
	token, ok := extractToken(r)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials"}`)
		return
	}

	fixture, exists := fixtures[token]
	if !exists {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials"}`)
		return
	}

	// Classic PATs include the X-OAuth-Scopes header.
	if fixture.IsClassic {
		w.Header().Set("X-OAuth-Scopes", "repo, user")
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"login": fixture.Login,
		"id":    fixture.ID,
		"email": fixture.Email,
	})
}

// handleCheckOrgMembership implements GET /orgs/{org}/members/{username}.
func handleCheckOrgMembership(w http.ResponseWriter, r *http.Request) {
	token, ok := extractToken(r)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials"}`)
		return
	}

	fixture, exists := fixtures[token]
	if !exists {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials"}`)
		return
	}

	username := r.PathValue("username")
	if fixture.Login != username {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if fixture.IsOrgMember {
		w.WriteHeader(http.StatusNoContent)
	} else {
		w.WriteHeader(http.StatusNotFound)
	}
}

// handleListUserTeams implements GET /user/teams.
func handleListUserTeams(w http.ResponseWriter, r *http.Request) {
	token, ok := extractToken(r)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials"}`)
		return
	}

	fixture, exists := fixtures[token]
	if !exists {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials"}`)
		return
	}

	type org struct {
		Login string `json:"login"`
	}
	type team struct {
		Slug         string `json:"slug"`
		Organization org    `json:"organization"`
	}

	teams := make([]team, 0, len(fixture.Teams))
	for _, slug := range fixture.Teams {
		teams = append(teams, team{
			Slug:         slug,
			Organization: org{Login: "test-org"},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(teams)
}
