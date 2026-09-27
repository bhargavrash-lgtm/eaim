package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/eami/api/internal/netguard"
	"github.com/eami/api/internal/store"
)

// WebhookTimeout bounds one webhook delivery end to end (dial, TLS,
// request, response headers). Slack answers in well under a second; this
// only has to stop a hung or deliberately slow target from pinning a
// goroutine.
const WebhookTimeout = 10 * time.Second

// NewWebhookClient returns the HTTP client every Slack webhook send uses:
// the tenant-supplied URL is dialed only through netguard's SSRF guard
// (no loopback, private, link-local or metadata addresses, DNS-rebinding
// safe), with WebhookTimeout, no proxy and no redirects (B-238).
func NewWebhookClient() *http.Client {
	return netguard.NewHTTPClient(netguard.DialContext, WebhookTimeout)
}

// ErrInvalidWebhookURL is returned by ValidateWebhookURL for every rejected
// URL. Deliberately one error for every cause (bad scheme, internal
// address, unresolvable host) so the save response can't be used to
// learn which internal hostnames exist.
var ErrInvalidWebhookURL = errors.New("webhook URL must be a public https URL")

// ValidateWebhookURL is the save-time check for a Slack webhook URL: https
// only, a host, no embedded credentials, and a host that resolves only to
// public addresses. Send time is still guarded independently by
// NewWebhookClient (a URL saved before this check existed, or DNS that
// changes after save, never reaches an internal address either way).
func ValidateWebhookURL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return ErrInvalidWebhookURL
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if netguard.IsBlocked(ip) {
			return ErrInvalidWebhookURL
		}
		return nil
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return ErrInvalidWebhookURL
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil || len(addrs) == 0 {
		return ErrInvalidWebhookURL
	}
	for _, a := range addrs {
		if netguard.IsBlocked(a.IP) {
			return ErrInvalidWebhookURL
		}
	}
	return nil
}

// SendSlack posts a Slack message to the given incoming webhook URL using
// client (production: NewWebhookClient()). The returned error is for
// server-side logging only -- it can carry dial/DNS detail about the
// target and must never be echoed to an API caller.
func SendSlack(ctx context.Context, client *http.Client, webhookURL, message string) error {
	type payload struct {
		Text string `json:"text"`
	}
	body, _ := json.Marshal(payload{Text: message})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		// The parse error would quote the (secret) URL; see below.
		return errors.New("slack webhook: invalid URL")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		// *url.Error's text embeds the full request URL, and the webhook
		// URL is a credential (openapi.yaml: never logged). Keep only the
		// operation and the underlying cause.
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("slack webhook %s: %w", ue.Op, ue.Err)
		}
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// BuildSlackMessage returns a formatted Slack message for a fired alert.
func BuildSlackMessage(rule store.AlertRule, metricValue float64) string {
	emojis := map[string]string{
		"info":     ":information_source:",
		"warning":  ":warning:",
		"high":     ":red_circle:",
		"critical": ":rotating_light:",
	}
	emoji := emojis[rule.Severity]
	if emoji == "" {
		emoji = ":bell:"
	}
	cfg, _ := ParseConditionConfig(rule.ConditionConfig)
	return fmt.Sprintf("%s *EAMI Alert — %s*\n> %s = %.2f (threshold: %.0f, window: %dm)\n> Severity: *%s*",
		emoji, rule.Name, cfg.Metric, metricValue, cfg.Threshold, cfg.WindowMinutes, rule.Severity)
}
