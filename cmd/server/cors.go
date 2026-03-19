package main

import (
	"strings"

	"github.com/philia-technologies/mayas-pharm/internal/config"
)

func resolveAllowedOrigins(cfg *config.Config) (string, bool) {
	raw := strings.TrimSpace(cfg.AllowedOrigins)
	if raw == "" {
		raw = strings.TrimSpace(cfg.AllowedOrigin)
	}
	if raw == "" {
		return "*", false
	}

	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		origin := strings.TrimSpace(part)
		if origin == "" {
			continue
		}
		if origin == "*" {
			return "*", false
		}
		if _, exists := seen[origin]; exists {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}

	if len(origins) == 0 {
		return "*", false
	}

	return strings.Join(origins, ","), true
}
