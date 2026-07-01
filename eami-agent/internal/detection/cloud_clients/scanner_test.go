package cloud_clients

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectEnvKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-abcdefghijklmnop")

	results, err := Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	var found bool
	for _, c := range results {
		if c.Provider == ProviderOpenAI && c.Source == "env" {
			found = true
			if c.KeyPrefix != "sk-abcd" {
				t.Errorf("key prefix: want %q, got %q", "sk-abcd", c.KeyPrefix)
			}
			if !c.Configured {
				t.Error("Configured should be true")
			}
		}
	}
	if !found {
		t.Error("OpenAI env key not detected")
	}
}

func TestKeyPrefix_Truncates(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"sk-1234567890", "sk-1234"},
		{"abc", "abc"},
		{"", ""},
		{`"quoted"`, "quoted"}, // quotes stripped
	}
	for _, tc := range cases {
		if got := keyPrefix(tc.in); got != tc.want {
			t.Errorf("keyPrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDetectConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config")
	os.WriteFile(cfgFile, []byte("OPENAI_API_KEY=sk-test12345\n"), 0o600)

	results := detectConfigFile(dir, []string{"OPENAI_API_KEY"}, ProviderOpenAI, "file")
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].KeyPrefix != "sk-test" {
		t.Errorf("key prefix: got %q", results[0].KeyPrefix)
	}
}

func TestGoogleVertex_EnvVar(t *testing.T) {
	// Write a temp "credentials" file and point GOOGLE_APPLICATION_CREDENTIALS at it.
	f, err := os.CreateTemp("", "adc-*.json")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", f.Name())

	results := detectGoogleVertex()
	if len(results) == 0 {
		t.Fatal("expected Google Vertex detection")
	}
	if results[0].Provider != ProviderGoogleVertex {
		t.Errorf("provider: got %q", results[0].Provider)
	}
	if results[0].Source != "env" {
		t.Errorf("source: want %q, got %q", "env", results[0].Source)
	}
}

func TestNoFullKeyLeaked(t *testing.T) {
	// Ensure the full key value never appears in any CloudClient field.
	fullKey := "sk-supersecretkey1234567890abcdef"
	t.Setenv("ANTHROPIC_API_KEY", fullKey)

	results, _ := Scan(context.Background())
	for _, c := range results {
		if c.Provider != ProviderAnthropic {
			continue
		}
		if c.KeyPrefix == fullKey {
			t.Error("full key leaked in KeyPrefix")
		}
		if len(c.KeyPrefix) > 7 {
			t.Errorf("KeyPrefix longer than 7 chars: %q", c.KeyPrefix)
		}
	}
}
