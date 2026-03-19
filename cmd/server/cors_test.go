package main

import (
	"testing"

	"github.com/philia-technologies/mayas-pharm/internal/config"
)

func TestResolveAllowedOrigins(t *testing.T) {
	tests := []struct {
		name            string
		cfg             *config.Config
		wantOrigins     string
		wantCredentials bool
	}{
		{
			name: "comma separated allowlist",
			cfg: &config.Config{
				AllowedOrigins: "http://localhost:3000, https://example.com, http://localhost:3000",
			},
			wantOrigins:     "http://localhost:3000,https://example.com",
			wantCredentials: true,
		},
		{
			name: "fallback single origin",
			cfg: &config.Config{
				AllowedOrigin: "https://legacy.example.com",
			},
			wantOrigins:     "https://legacy.example.com",
			wantCredentials: true,
		},
		{
			name: "wildcard disables credentials",
			cfg: &config.Config{
				AllowedOrigins: "*",
			},
			wantOrigins:     "*",
			wantCredentials: false,
		},
		{
			name:            "empty falls back to wildcard",
			cfg:             &config.Config{},
			wantOrigins:     "*",
			wantCredentials: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotOrigins, gotCredentials := resolveAllowedOrigins(tc.cfg)
			if gotOrigins != tc.wantOrigins {
				t.Fatalf("resolveAllowedOrigins() origins = %q, want %q", gotOrigins, tc.wantOrigins)
			}
			if gotCredentials != tc.wantCredentials {
				t.Fatalf("resolveAllowedOrigins() credentials = %v, want %v", gotCredentials, tc.wantCredentials)
			}
		})
	}
}
