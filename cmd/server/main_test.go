// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"testing"
	"time"
)

func TestParseFlags_Defaults(t *testing.T) {
	cfg, err := parseFlags([]string{"-org", "my-org"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Org != "my-org" {
		t.Errorf("Org = %q, want %q", cfg.Org, "my-org")
	}
	if cfg.Listen != ":8080" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, ":8080")
	}
	if cfg.CacheTTL != 5*time.Minute {
		t.Errorf("CacheTTL = %v, want %v", cfg.CacheTTL, 5*time.Minute)
	}
	if cfg.RejectClassicPATs != true {
		t.Errorf("RejectClassicPATs = %v, want %v", cfg.RejectClassicPATs, true)
	}
	if cfg.CacheMaxSize != 1000 {
		t.Errorf("CacheMaxSize = %d, want %d", cfg.CacheMaxSize, 1000)
	}
}

func TestParseFlags_CustomValues(t *testing.T) {
	args := []string{
		"-org", "custom-org",
		"-listen", ":9090",
		"-cache-ttl", "10m",
		"-cache-max-size", "500",
		"-reject-classic-pats=false",
	}

	cfg, err := parseFlags(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Org != "custom-org" {
		t.Errorf("Org = %q, want %q", cfg.Org, "custom-org")
	}
	if cfg.Listen != ":9090" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, ":9090")
	}
	if cfg.CacheTTL != 10*time.Minute {
		t.Errorf("CacheTTL = %v, want %v", cfg.CacheTTL, 10*time.Minute)
	}
	if cfg.CacheMaxSize != 500 {
		t.Errorf("CacheMaxSize = %d, want %d", cfg.CacheMaxSize, 500)
	}
	if cfg.RejectClassicPATs != false {
		t.Errorf("RejectClassicPATs = %v, want %v", cfg.RejectClassicPATs, false)
	}
}

func TestParseFlags_OrgRequired(t *testing.T) {
	_, err := parseFlags([]string{})
	if err == nil {
		t.Fatal("expected error when -org is not provided, got nil")
	}
}

func TestParseFlags_OrgEmpty(t *testing.T) {
	_, err := parseFlags([]string{"-org", ""})
	if err == nil {
		t.Fatal("expected error when -org is empty, got nil")
	}
}

func TestParseFlags_InvalidFlag(t *testing.T) {
	_, err := parseFlags([]string{"-org", "my-org", "-nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown flag, got nil")
	}
}

func TestParseFlags_NegativeCacheTTL(t *testing.T) {
	_, err := parseFlags([]string{"-org", "my-org", "-cache-ttl", "-1s"})
	if err == nil {
		t.Fatal("expected error for negative cache-ttl, got nil")
	}
}

func TestParseFlags_ZeroCacheTTL(t *testing.T) {
	cfg, err := parseFlags([]string{"-org", "my-org", "-cache-ttl", "0s"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CacheTTL != 0 {
		t.Errorf("CacheTTL = %v, want 0", cfg.CacheTTL)
	}
}

func TestParseFlags_HelpFlag(t *testing.T) {
	_, err := parseFlags([]string{"-help"})
	if err == nil {
		t.Fatal("expected error for -help flag, got nil")
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: Config{
				Org:               "my-org",
				Listen:            ":8080",
				CacheTTL:          5 * time.Minute,
				CacheMaxSize:      1000,
				RejectClassicPATs: true,
			},
			wantErr: false,
		},
		{
			name: "missing org",
			cfg: Config{
				Org:          "",
				Listen:       ":8080",
				CacheTTL:     5 * time.Minute,
				CacheMaxSize: 1000,
			},
			wantErr: true,
		},
		{
			name: "negative cache TTL",
			cfg: Config{
				Org:          "my-org",
				Listen:       ":8080",
				CacheTTL:     -1 * time.Second,
				CacheMaxSize: 1000,
			},
			wantErr: true,
		},
		{
			name: "zero cache TTL is valid",
			cfg: Config{
				Org:          "my-org",
				Listen:       ":8080",
				CacheTTL:     0,
				CacheMaxSize: 1000,
			},
			wantErr: false,
		},
		{
			name: "zero cache max size",
			cfg: Config{
				Org:          "my-org",
				Listen:       ":8080",
				CacheTTL:     5 * time.Minute,
				CacheMaxSize: 0,
			},
			wantErr: true,
		},
		{
			name: "negative cache max size",
			cfg: Config{
				Org:          "my-org",
				Listen:       ":8080",
				CacheTTL:     5 * time.Minute,
				CacheMaxSize: -1,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
