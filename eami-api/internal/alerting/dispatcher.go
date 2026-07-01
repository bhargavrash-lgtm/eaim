package alerting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/eami/api/internal/store"
)

// SendSlack posts a Slack message to the given incoming webhook URL.
func SendSlack(webhookURL, message string) error {
	type payload struct {
		Text string `json:"text"`
	}
	body, _ := json.Marshal(payload{Text: message})
	resp, err := http.Post(webhookURL, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
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
