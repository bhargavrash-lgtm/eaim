// Package cloud_clients detects configured AI cloud API clients on the endpoint.
// Security: only credential presence and a 7-char key prefix are reported.
// Full key values are never stored, logged, or transmitted.
package cloud_clients

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
)

type Provider string

const (
	ProviderOpenAI       Provider = "openai"
	ProviderAnthropic    Provider = "anthropic"
	ProviderAWSBedrock   Provider = "aws_bedrock"
	ProviderAzureOpenAI  Provider = "azure_openai"
	ProviderGoogleVertex Provider = "google_vertex"
)

// CloudClient describes a detected AI cloud credential.
type CloudClient struct {
	Provider   Provider `json:"provider"`
	Configured bool     `json:"configured"`
	KeyPrefix  string   `json:"key_prefix,omitempty"` // first 7 chars only
	Source     string   `json:"source"`               // env, file
}

// Scan detects configured AI cloud API clients. Safe to call concurrently.
func Scan(_ context.Context) ([]CloudClient, error) {
	var results []CloudClient

	results = append(results, detectEnvKey("OPENAI_API_KEY", ProviderOpenAI, "env")...)
	results = append(results, detectConfigFile(
		filepath.Join(userHome(), ".config", "openai"), []string{"OPENAI_API_KEY"},
		ProviderOpenAI, "file")...)

	results = append(results, detectEnvKey("ANTHROPIC_API_KEY", ProviderAnthropic, "env")...)
	results = append(results, detectConfigFile(
		filepath.Join(userHome(), ".config", "anthropic"), []string{"ANTHROPIC_API_KEY"},
		ProviderAnthropic, "file")...)

	results = append(results, detectAWSBedrock()...)

	results = append(results, detectEnvKey("AZURE_OPENAI_API_KEY", ProviderAzureOpenAI, "env")...)
	results = append(results, detectAzureFile()...)

	results = append(results, detectGoogleVertex()...)

	return results, nil
}

func detectEnvKey(envVar string, provider Provider, source string) []CloudClient {
	val := os.Getenv(envVar)
	if val == "" {
		return nil
	}
	return []CloudClient{{Provider: provider, Configured: true, KeyPrefix: keyPrefix(val), Source: source}}
}

func detectConfigFile(dir string, keys []string, provider Provider, source string) []CloudClient {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if c, ok := parseKeyFile(filepath.Join(dir, e.Name()), keys, provider, source); ok {
			return []CloudClient{c}
		}
	}
	return nil
}

func parseKeyFile(path string, keys []string, provider Provider, source string) (CloudClient, bool) {
	f, err := os.Open(path)
	if err != nil {
		return CloudClient{}, false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, k := range keys {
			if strings.HasPrefix(line, k+"=") || strings.HasPrefix(line, k+" =") {
				parts := strings.SplitN(line, "=", 2)
				var prefix string
				if len(parts) == 2 {
					prefix = keyPrefix(strings.TrimSpace(parts[1]))
				}
				return CloudClient{Provider: provider, Configured: true, KeyPrefix: prefix, Source: source}, true
			}
		}
	}
	return CloudClient{}, false
}

func detectAWSBedrock() []CloudClient {
	creds := filepath.Join(userHome(), ".aws", "credentials")
	if _, err := os.Stat(creds); os.IsNotExist(err) {
		return nil
	}
	return []CloudClient{{Provider: ProviderAWSBedrock, Configured: true, Source: "file"}}
}

func detectAzureFile() []CloudClient {
	if _, err := os.Stat(filepath.Join(userHome(), ".azure")); os.IsNotExist(err) {
		return nil
	}
	return []CloudClient{{Provider: ProviderAzureOpenAI, Configured: true, Source: "file"}}
}

func detectGoogleVertex() []CloudClient {
	if gac := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); gac != "" {
		if _, err := os.Stat(gac); err == nil {
			return []CloudClient{{Provider: ProviderGoogleVertex, Configured: true, Source: "env"}}
		}
	}
	adc := adcPath()
	if _, err := os.Stat(adc); err == nil {
		return []CloudClient{{Provider: ProviderGoogleVertex, Configured: true, Source: "file"}}
	}
	return nil
}

func adcPath() string {
	if appData := os.Getenv("APPDATA"); appData != "" {
		return filepath.Join(appData, "gcloud", "application_default_credentials.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")
}

func keyPrefix(key string) string {
	key = strings.Trim(key, `"'`)
	if len(key) == 0 {
		return ""
	}
	if len(key) <= 7 {
		return key
	}
	return key[:7]
}

func userHome() string {
	if v := os.Getenv("USERPROFILE"); v != "" {
		return v
	}
	h, _ := os.UserHomeDir()
	return h
}
