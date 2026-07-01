package api

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// ConfigProxyHandler handles GET /v1/agent-config/{agent_id}.
// It proxies the request to the SaaS API and returns the config JSON directly
// to the agent. A missing saasURL causes the handler to return 503.
func ConfigProxyHandler(saasURL, serviceKey string, client *http.Client, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.PathValue("agent_id")
		if agentID == "" {
			jsonError(w, "missing agent_id", http.StatusBadRequest)
			return
		}

		if saasURL == "" {
			// SaaS URL not configured — collector is running in standalone mode.
			jsonError(w, "remote config unavailable: saas_url not configured", http.StatusServiceUnavailable)
			return
		}

		upstream := strings.TrimRight(saasURL, "/") + "/v1/agents/" + agentID + "/config"
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream, nil)
		if err != nil {
			log.Error("config proxy: build request", "err", err, "agent_id", agentID)
			jsonError(w, "internal error", http.StatusInternalServerError)
			return
		}
		if serviceKey != "" {
			req.Header.Set("X-Service-Key", serviceKey)
		}

		resp, err := client.Do(req)
		if err != nil {
			log.Warn("config proxy: upstream error", "err", err, "agent_id", agentID)
			jsonError(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// Proxy status and body verbatim. The agent interprets 404 as
		// "not yet registered" and 200 as a config update.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}
