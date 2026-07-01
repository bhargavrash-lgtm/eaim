// Command eami-collector is the on-prem HTTP receiver that buffers and
// forwards agent reports to the SaaS API.
package main

import (
	"context"
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
	handler := api.Router(database, staticKey, cfg.Forwarder.SAASURL, cfg.Forwarder.APIKey, log)
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
