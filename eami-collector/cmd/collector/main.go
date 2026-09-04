// Command eami-collector is the on-prem HTTP receiver that buffers and
// forwards agent reports to the SaaS API.
//
// It also doubles as the admin CLI for minting/revoking/listing per-agent
// API keys (B-073) — a subcommand as the first argument (mint-key,
// revoke-key, list-keys) runs that action against the buffer DB and exits,
// instead of starting the HTTP server. This lets an admin run, e.g.,
// `docker compose exec eami-collector /app/collector mint-key --agent-id
// workstation-42 --label "Office floor 3"` directly against the running
// container's own data volume — no extra tools needed (unlike
// scripts/create_api_key.sh, which needs sqlite3/openssl/python3, none of
// which are present in the minimal Alpine runtime image).
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/eami/collector/internal/api"
	"github.com/eami/collector/internal/db"
	"github.com/eami/collector/internal/forwarder"
	"gopkg.in/yaml.v3"
)

var cfgPath = flag.String("config", "eami-collector.yaml", "path to config file")

type config struct {
	Collector struct {
		ListenPort  int    `yaml:"listen_port"`
		TLSCertPath string `yaml:"tls_cert_path"`
		TLSKeyPath  string `yaml:"tls_key_path"`
	} `yaml:"collector"`
	Buffer struct {
		DBPath  string `yaml:"db_path"`
		MaxRows int    `yaml:"max_rows"`
	} `yaml:"buffer"`
	Forwarder forwarder.Config `yaml:"forwarder"`
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "mint-key":
			runMintKey(os.Args[2:])
			return
		case "revoke-key":
			runRevokeKey(os.Args[2:])
			return
		case "list-keys":
			runListKeys(os.Args[2:])
			return
		}
	}
	runServer()
}

// runServer is the original main() body, unchanged in behavior — extracted
// so the CLI subcommands above can exit before ever touching flag.Parse()'s
// server-oriented flag set or starting the HTTP listener.
func runServer() {
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg := &config{}
	cfg.Collector.ListenPort = 8888
	cfg.Buffer.DBPath = "./data/buffer.db"
	cfg.Buffer.MaxRows = 100_000

	if f, err := os.Open(*cfgPath); err == nil {
		if err := yaml.NewDecoder(f).Decode(cfg); err != nil {
			log.Error("config decode failed", "err", err)
			os.Exit(1)
		}
		f.Close()
	}

	// Environment variable overrides (docker-compose / Kubernetes style).
	if v := os.Getenv("COLLECTOR_BUFFER_DB_PATH"); v != "" {
		cfg.Buffer.DBPath = v
	}
	if v := os.Getenv("COLLECTOR_LISTEN_PORT"); v != "" {
		var port int
		if _, err := fmt.Sscanf(v, "%d", &port); err == nil {
			cfg.Collector.ListenPort = port
		}
	}
	if v := os.Getenv("COLLECTOR_SAAS_URL"); v != "" {
		cfg.Forwarder.SAASURL = v
	}
	if v := os.Getenv("COLLECTOR_API_KEY"); v != "" {
		cfg.Forwarder.APIKey = v
	}
	if v := os.Getenv("COLLECTOR_SERVICE_KEY"); v != "" {
		cfg.Forwarder.ServiceKey = v
	}

	database, err := db.Open(cfg.Buffer.DBPath)
	if err != nil {
		log.Error("db open failed", "err", err)
		os.Exit(1)
	}
	defer database.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	fwd := forwarder.New(cfg.Forwarder, database, log)
	go func() {
		if err := fwd.Run(ctx); err != nil && err != context.Canceled {
			log.Error("forwarder exited", "err", err)
		}
	}()

	staticKey := cfg.Forwarder.APIKey
	handler := api.Router(database, staticKey, cfg.Forwarder.SAASURL, cfg.Forwarder.ServiceKey, log)
	addr := fmt.Sprintf(":%d", cfg.Collector.ListenPort)
	srv := &http.Server{Addr: addr, Handler: handler}

	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()

	log.Info("eami-collector listening", "addr", addr)
	if cfg.Collector.TLSCertPath != "" && cfg.Collector.TLSKeyPath != "" {
		err = srv.ListenAndServeTLS(cfg.Collector.TLSCertPath, cfg.Collector.TLSKeyPath)
	} else {
		err = srv.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		log.Error("server error", "err", err)
		os.Exit(1)
	}
}

// resolveDBPath resolves the buffer DB path a CLI subcommand should open,
// at the exact same precedence runServer itself applies (env var overrides
// the config file, which overrides the hardcoded default) plus one CLI-only
// override on top: explicit (--db) wins over everything, for the rare case
// an admin wants to point at a specific file regardless of the running
// server's own configuration.
//
// configPath is read the same way runServer reads *cfgPath — this is what
// closes a real gap a code-review pass on this brief's own diff caught: an
// earlier version of this function only checked COLLECTOR_BUFFER_DB_PATH,
// silently ignoring a deployment that instead sets buffer.db_path in
// eami-collector.yaml (a config style runServer has always supported) —
// running mint-key without --db in that setup would have opened/created a
// DIFFERENT SQLite file than the one the running server actually reads,
// with no error, no warning: a newly minted key would silently never work.
func resolveDBPath(explicit, configPath string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv("COLLECTOR_BUFFER_DB_PATH"); v != "" {
		return v
	}
	return configuredOrDefaultDBPath(configPath)
}

// configuredOrDefaultDBPath reads buffer.db_path from the YAML config file
// at configPath, if present, else returns runServer's own hardcoded
// "./data/buffer.db" default. A missing/unreadable/unparseable config file
// is not an error here — identical to runServer's own tolerance of a
// missing config file (it only started before this brief because a fresh
// deployment has no eami-collector.yaml on disk at all and relies entirely
// on env vars, a case this function must keep working exactly the same
// way).
func configuredOrDefaultDBPath(configPath string) string {
	const defaultDBPath = "./data/buffer.db"
	f, err := os.Open(configPath)
	if err != nil {
		return defaultDBPath
	}
	defer f.Close()
	var cfg config
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil || cfg.Buffer.DBPath == "" {
		return defaultDBPath
	}
	return cfg.Buffer.DBPath
}

// randomKey generates a cryptographically random per-agent API key,
// matching scripts/create_api_key.sh's own "eami_k_<64 hex chars>" format
// (that script uses "eami-", this uses "eami_k_" to match the placeholder
// already documented in eami-agent/installer/Product.wxs's own top comment
// — cosmetic, both are opaque bearer tokens to the collector either way).
func randomKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "eami_k_" + hex.EncodeToString(b), nil
}

func runMintKey(args []string) {
	fs := flag.NewFlagSet("mint-key", flag.ExitOnError)
	agentID := fs.String("agent-id", "", "the agent identity this key authenticates as (required — must match the value the agent reports as agent_id, e.g. its hostname)")
	label := fs.String("label", "", "a human-readable description (e.g. \"Office floor 3, workstation 42\")")
	dbFlag := fs.String("db", "", "path to the collector's SQLite buffer DB (default: same file the running server uses -- see -config)")
	configFlag := fs.String("config", "eami-collector.yaml", "path to the server's config file, read for buffer.db_path when --db and $COLLECTOR_BUFFER_DB_PATH are both unset (matches the server's own -config default)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	dbPath := resolveDBPath(*dbFlag, *configFlag)
	if *agentID == "" {
		fmt.Fprintln(os.Stderr, "mint-key: --agent-id is required")
		os.Exit(1)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mint-key: open db: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	rawKey, err := randomKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mint-key: generate key: %v\n", err)
		os.Exit(1)
	}
	if err := api.RegisterKey(database, rawKey, *agentID, *label); err != nil {
		fmt.Fprintf(os.Stderr, "mint-key: %v\n"+
			"(if this agent already has an active key, revoke it first with revoke-key --agent-id %s)\n", err, *agentID)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("API key created:")
	fmt.Printf("  Agent ID: %s\n", *agentID)
	fmt.Printf("  Label:    %s\n", *label)
	fmt.Printf("  Key:      %s\n", rawKey)
	fmt.Println()
	fmt.Println("Store this key securely — it will not be shown again.")
	fmt.Printf("Configure the agent with: collector.api_key: %s\n", rawKey)
}

func runRevokeKey(args []string) {
	fs := flag.NewFlagSet("revoke-key", flag.ExitOnError)
	agentID := fs.String("agent-id", "", "the agent identity whose active key should be revoked (required)")
	dbFlag := fs.String("db", "", "path to the collector's SQLite buffer DB (default: same file the running server uses -- see -config)")
	configFlag := fs.String("config", "eami-collector.yaml", "path to the server's config file, read for buffer.db_path when --db and $COLLECTOR_BUFFER_DB_PATH are both unset (matches the server's own -config default)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	dbPath := resolveDBPath(*dbFlag, *configFlag)
	if *agentID == "" {
		fmt.Fprintln(os.Stderr, "revoke-key: --agent-id is required")
		os.Exit(1)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "revoke-key: open db: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	n, err := api.RevokeKey(database, *agentID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "revoke-key: %v\n", err)
		os.Exit(1)
	}
	if n == 0 {
		// Exit 0, not 1 -- "no active key to revoke" is the same end state
		// the caller wanted (this agent has no active key), not a failure.
		// Kept as exit 0 specifically so this command is safe to call
		// idempotently from automation (e.g. a decommission script that
		// revokes defensively without first checking whether a key exists)
		// without a strict-mode (`set -e`-style) caller aborting on a
		// perfectly normal outcome (code-review finding on this brief).
		fmt.Printf("no active key found for agent %q (already revoked, or never existed)\n", *agentID)
		return
	}
	fmt.Printf("revoked active key for agent %q\n", *agentID)
}

func runListKeys(args []string) {
	fs := flag.NewFlagSet("list-keys", flag.ExitOnError)
	dbFlag := fs.String("db", "", "path to the collector's SQLite buffer DB (default: same file the running server uses -- see -config)")
	configFlag := fs.String("config", "eami-collector.yaml", "path to the server's config file, read for buffer.db_path when --db and $COLLECTOR_BUFFER_DB_PATH are both unset (matches the server's own -config default)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	dbPath := resolveDBPath(*dbFlag, *configFlag)

	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list-keys: open db: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	keys, err := api.ListKeys(database)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list-keys: %v\n", err)
		os.Exit(1)
	}
	if len(keys) == 0 {
		fmt.Println("no keys registered")
		return
	}
	fmt.Printf("%-20s %-30s %-20s %s\n", "AGENT ID", "LABEL", "CREATED", "STATUS")
	for _, k := range keys {
		status := "active"
		if k.RevokedAt != "" {
			status = "revoked " + k.RevokedAt
		}
		fmt.Printf("%-20s %-30s %-20s %s\n", k.AgentID, k.Label, k.CreatedAt, status)
	}
}
