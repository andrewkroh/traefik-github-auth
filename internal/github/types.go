// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

// Package github provides types and a client for interacting with the GitHub API.
package github

// User represents a GitHub user profile.
type User struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
}

// Team represents a GitHub team.
type Team struct {
	Slug         string       `json:"slug"`
	Organization Organization `json:"organization"`
}

// Organization represents a GitHub organization.
type Organization struct {
	Login string `json:"login"`
}
