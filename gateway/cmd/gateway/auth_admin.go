package gateway

import (
	"context"
	"net/http"
	"time"
)

func (g *Gateway) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		g.reject(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, authStatusJSON(g.authHealth()))
}

func (g *Gateway) handleAuthReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		g.reject(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.reloadCredentials()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	_ = g.validateAuth(ctx)
	writeJSON(w, http.StatusOK, authStatusJSON(g.authHealth()))
}

func authStatusJSON(state authHealthState) map[string]any {
	metadata := state.Metadata
	return map[string]any{
		"status":              state.Health,
		"source":              state.Source,
		"label":               metadata.Label,
		"limit":               metadata.Limit,
		"limit_remaining":     metadata.LimitRemaining,
		"limit_reset":         metadata.LimitReset,
		"is_free_tier":        metadata.IsFreeTier,
		"is_management_key":   metadata.IsManagementKey,
		"is_provisioning_key": metadata.IsProvisioningKey,
		"expires_at":          metadata.ExpiresAt,
		"last_error":          state.LastError,
		"last_error_at":       rfc3339OrEmpty(state.LastErrorAt),
		"last_ok_at":          rfc3339OrEmpty(state.LastOKAt),
	}
}
