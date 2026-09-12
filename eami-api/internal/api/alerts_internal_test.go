// alerts_internal_test.go -- eami-api/internal/api
// Pure unit tests for validateAlertRuleReq (no Postgres needed). Pins the
// exact accepted metric/severity/window sets at the validator level so any
// future drift between this file and the frontend's MetricKey/severity enums
// is caught here first, not discovered live in the UI as B-184 was.
package api

import "testing"

func TestValidateAlertRuleReq_Metrics(t *testing.T) {
	validMetrics := []string{
		"denied_actions_count", "escalated_actions_count", "scope_drift_count",
		"new_endpoints_count", "token_spend_usd", "failed_delivery_count",
	}
	for _, m := range validMetrics {
		t.Run(m, func(t *testing.T) {
			if err := validateAlertRuleReq("rule", m, "gt", 60, "high"); err != nil {
				t.Errorf("expected valid metric %q to pass, got: %v", m, err)
			}
		})
	}

	// B-184 regression: the frontend's OLD unsuffixed metric keys (denied_actions,
	// escalated_actions, scope_drift, new_endpoints, failed_deliveries) must NOT
	// validate -- proves the backend's canonical set stays the suffixed form.
	invalidMetrics := []string{
		"denied_actions", "escalated_actions", "scope_drift",
		"new_endpoints", "failed_deliveries", "", "bogus_metric",
	}
	for _, m := range invalidMetrics {
		t.Run("invalid_"+m, func(t *testing.T) {
			if err := validateAlertRuleReq("rule", m, "gt", 60, "high"); err == nil {
				t.Errorf("expected invalid metric %q to fail, got nil error", m)
			}
		})
	}
}

func TestValidateAlertRuleReq_Severities(t *testing.T) {
	// B-184 regression: info/warning/high/critical is the canonical severity
	// set used everywhere else in the app (AlertRule/Alert read schemas,
	// SeverityBadge) -- the OpenAPI write-schema's former low/medium/high/
	// critical enum was the actual bug, not this validator.
	validSeverities := []string{"info", "warning", "high", "critical"}
	for _, sev := range validSeverities {
		t.Run(sev, func(t *testing.T) {
			if err := validateAlertRuleReq("rule", "token_spend_usd", "gt", 60, sev); err != nil {
				t.Errorf("expected valid severity %q to pass, got: %v", sev, err)
			}
		})
	}

	invalidSeverities := []string{"low", "medium", "", "bogus"}
	for _, sev := range invalidSeverities {
		t.Run("invalid_"+sev, func(t *testing.T) {
			if err := validateAlertRuleReq("rule", "token_spend_usd", "gt", 60, sev); err == nil {
				t.Errorf("expected invalid severity %q to fail, got nil error", sev)
			}
		})
	}
}

func TestValidateAlertRuleReq_Windows(t *testing.T) {
	validWindows := []int{5, 15, 60, 1440}
	for _, w := range validWindows {
		if err := validateAlertRuleReq("rule", "token_spend_usd", "gt", w, "high"); err != nil {
			t.Errorf("expected valid window %d to pass, got: %v", w, err)
		}
	}

	// B-184 regression: 0 is exactly the Go zero-value that resulted from the
	// old "window" (openapi/frontend) vs "window_minutes" (this struct's json
	// tag) field-name mismatch -- must still be rejected, not silently coerced
	// into a valid window.
	invalidWindows := []int{0, 1, 30, -5}
	for _, w := range invalidWindows {
		if err := validateAlertRuleReq("rule", "token_spend_usd", "gt", w, "high"); err == nil {
			t.Errorf("expected invalid window %d to fail, got nil error", w)
		}
	}
}

func TestValidateAlertRuleReq_ConditionAndName(t *testing.T) {
	if err := validateAlertRuleReq("", "token_spend_usd", "gt", 60, "high"); err == nil {
		t.Error("expected empty name to fail")
	}
	if err := validateAlertRuleReq("rule", "token_spend_usd", "lt", 60, "high"); err == nil {
		t.Error("expected non-gt condition to fail (only \"gt\" is supported)")
	}
	if err := validateAlertRuleReq("rule", "token_spend_usd", "", 60, "high"); err == nil {
		t.Error("expected empty condition to fail")
	}
}
