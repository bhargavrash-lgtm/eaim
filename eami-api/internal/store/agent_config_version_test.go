package store

import "testing"

// Golden vectors for the remote-config version (B-293). eami-agent's
// internal/remoteconfig pins the same three inputs to the same three
// outputs; if either side's canonical form drifts, both tests must change
// together or agents will reject every config as version_mismatch.
var configVersionGolden = []struct {
	name string
	cfg  AgentConfig
	want string
}{
	{"defaults", AgentConfig{
		ScanIntervalSeconds: 300,
		EnabledScanners:     []string{"ai_apps", "models", "mcp_servers", "cloud_clients", "network_activity", "browser", "ai_processes", "gpu", "python_envs", "nodejs_ai"},
		ModelScanPaths:      []string{"/home", "/Users", `C:\Users`},
		ModelFileSizeMB:     100,
		MaxReportSizeBytes:  5242880,
	}, "c1:e13b658a7e5071ab4d196d1dc42eb1b32d15041f075bf12a4db95cac0cc8b8ad"},
	{"unsorted, duplicated, empty paths", AgentConfig{
		ScanIntervalSeconds: 3600,
		EnabledScanners:     []string{"gpu", "ai_apps", "gpu"},
		ModelScanPaths:      []string{},
		ModelFileSizeMB:     250,
		MaxReportSizeBytes:  1048576,
	}, "c1:d8d8341e266e8f725147e368c5d0bb8e34a9d8028d39f235b92b03fa1b79c958"},
	{"escaping", AgentConfig{
		ScanIntervalSeconds: 60,
		EnabledScanners:     nil,
		ModelScanPaths:      []string{`/opt/<m>&"q"`, "D:/Models"},
		ModelFileSizeMB:     1,
		MaxReportSizeBytes:  52428800,
	}, "c1:5cbc77f53148d018dfeb6a6b93b492f7d18a794e64b807f2e40b013c45e3d9f9"},
}

func TestConfigVersion_GoldenVectors(t *testing.T) {
	for _, g := range configVersionGolden {
		if got := g.cfg.Version(); got != g.want {
			t.Errorf("%s: Version() = %s, want %s", g.name, got, g.want)
		}
	}
}

func TestConfigVersion_OrderAndDuplicatesDontMatter(t *testing.T) {
	a := AgentConfig{ScanIntervalSeconds: 300, EnabledScanners: []string{"a", "b"}, ModelScanPaths: []string{"/x", "/y"}, ModelFileSizeMB: 1, MaxReportSizeBytes: 1}
	b := AgentConfig{ScanIntervalSeconds: 300, EnabledScanners: []string{"b", "a", "a"}, ModelScanPaths: []string{"/y", "/x"}, ModelFileSizeMB: 1, MaxReportSizeBytes: 1}
	if a.Version() != b.Version() {
		t.Fatal("list order or duplicates changed the version")
	}
	c := b
	c.ModelFileSizeMB = 2
	if c.Version() == b.Version() {
		t.Fatal("a real change kept the same version")
	}
	if (AgentConfig{}).Version() != (AgentConfig{EnabledScanners: []string{}, ModelScanPaths: []string{}}).Version() {
		t.Fatal("nil and [] lists hash differently")
	}
}
