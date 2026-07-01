package network_activity

import (
	"strings"
	"testing"
	"time"
)

func TestMatchesKnownAIHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"api.openai.com", true},
		{"API.OPENAI.COM", true},             // case-insensitive
		{"chat.openai.com", false},           // not a known endpoint
		{"api.anthropic.com", true},
		{"sub.api.anthropic.com", true},      // subdomain match
		{"generativelanguage.googleapis.com", true},
		{"api.cohere.com", true},
		{"api.mistral.ai", true},
		{"inference.ai.azure.com", true},
		{"bedrock-runtime.us-east-1.amazonaws.com", true},  // wildcard Bedrock
		{"bedrock-runtime.amazonaws.com", true},
		{"s3.amazonaws.com", false},           // not Bedrock
		{"google.com", false},
		{"openai.evil.com", false},            // suffix must match
	}
	for _, tc := range cases {
		if got := MatchesKnownAIHost(tc.host); got != tc.want {
			t.Errorf("MatchesKnownAIHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestParseDNSCache_BasicParsing(t *testing.T) {
	// Simulated ipconfig /displaydns output
	output := `
Windows IP Configuration

    api.openai.com
    ----------------------------------------
    Record Name . . . . . : api.openai.com
    Record Type . . . . . : 1
    Time To Live  . . . . : 123
    Data Length . . . . . : 4
    Section . . . . . . . : Answer
    A (Host) Record . . . : 104.18.7.192

    api.anthropic.com
    ----------------------------------------
    Record Name . . . . . : api.anthropic.com

    google.com
    ----------------------------------------
    Record Name . . . . . : google.com
`
	hits := ParseDNSCache(output)

	found := map[string]bool{}
	for _, h := range hits {
		found[h.Hostname] = true
		if h.DetectedAt.IsZero() {
			t.Errorf("DetectedAt should not be zero for %q", h.Hostname)
		}
	}

	if !found["api.openai.com"] {
		t.Error("expected api.openai.com in hits")
	}
	if !found["api.anthropic.com"] {
		t.Error("expected api.anthropic.com in hits")
	}
	if found["google.com"] {
		t.Error("google.com should not be in hits")
	}
}

func TestParseDNSCache_Deduplication(t *testing.T) {
	// Same hostname appearing multiple times should produce one hit.
	output := strings.Repeat("    Record Name . . . . . : api.openai.com\n", 5)
	hits := ParseDNSCache(output)
	count := 0
	for _, h := range hits {
		if h.Hostname == "api.openai.com" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 deduplicated hit, got %d", count)
	}
}

func TestParseDNSCache_Empty(t *testing.T) {
	hits := ParseDNSCache("")
	if len(hits) != 0 {
		t.Errorf("expected 0 hits for empty input, got %d", len(hits))
	}
}

func TestParseDNSCache_TimestampNow(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	output := "    Record Name . . . . . : api.openai.com\n"
	hits := ParseDNSCache(output)
	after := time.Now().UTC().Add(time.Second)
	for _, h := range hits {
		if h.DetectedAt.Before(before) || h.DetectedAt.After(after) {
			t.Errorf("DetectedAt %v out of expected range", h.DetectedAt)
		}
	}
}
