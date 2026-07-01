package mcp

// PolicyDeniedError is returned by the DecisionHandler when a policy rule
// explicitly denies the action. It is a sentinel type so the MCP handler can
// distinguish a structured policy denial from generic internal errors and build
// a correctly shaped JSON-RPC error response (code -32600, data.reason, data.policy_id).
type PolicyDeniedError struct {
	Reason   string // human-readable explanation from the policy engine
	PolicyID string // UUID of the matching rule, empty string when no rule matched
}

// Error implements the error interface.
func (e *PolicyDeniedError) Error() string {
	if e.PolicyID != "" {
		return "policy denied [" + e.PolicyID + "]: " + e.Reason
	}
	return "policy denied: " + e.Reason
}
