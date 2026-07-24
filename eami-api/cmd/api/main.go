// Command api is the EAMI SaaS REST API server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eami/api/internal/alerting"
	"github.com/eami/api/internal/api"
	authpkg "github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/config"
	"github.com/eami/api/internal/store"
)

func main() {
	cfgPath := flag.String("config", "eami-api.yaml", "path to YAML config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("eami-api: load config: %v", err)
	}

	// -- Database pool --------------------------------------------------------
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	pool, err := store.NewPool(ctx, cfg.Database.DSN)
	cancel()
	if err != nil {
		log.Fatalf("eami-api: connect to database: %v", err)
	}
	defer pool.Close()
	log.Println("eami-api: database connected")

	// -- Auth service ---------------------------------------------------------
	accessTTL := time.Duration(cfg.Auth.AccessTokenTTLSeconds) * time.Second
	refreshTTL := time.Duration(cfg.Auth.RefreshTokenTTLSeconds) * time.Second
	authSvc, err := authpkg.NewService(cfg.Auth.RSAPrivateKeyPath, accessTTL, refreshTTL)
	if err != nil {
		log.Fatalf("eami-api: init auth service: %v", err)
	}
	if cfg.Auth.RSAPrivateKeyPath == "" {
		log.Println("eami-api: WARNING -- using ephemeral RSA key (dev mode). Set rsa_private_key_path in config for production.")
	} else {
		log.Printf("eami-api: using persistent RSA key at %s", cfg.Auth.RSAPrivateKeyPath)
	}

	// -- Alerting engine ------------------------------------------------------
	queries := store.New(pool)
	engine := alerting.NewEngine(queries, cfg.Collector.URL, cfg.Collector.APIKey)

	// -- HTTP server ----------------------------------------------------------
	srv := api.NewServer(queries, authSvc, engine, cfg)

	httpSrv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      srv.Handler(),
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeoutSeconds) * time.Second,
		IdleTimeout:  time.Duration(cfg.Server.IdleTimeoutSeconds) * time.Second,
	}

	// -- Graceful shutdown ----------------------------------------------------
	serverCtx, serverCancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		sig := <-quit
		log.Printf("eami-api: received signal %s -- shutting down", sig)

		// Cancel the server context so the alerting engine stops.
		serverCancel()

		shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutCancel()
		if err := httpSrv.Shutdown(shutCtx); err != nil {
			log.Printf("eami-api: shutdown error: %v", err)
		}
		close(done)
	}()

	// Start alerting engine in background.
	go engine.Run(serverCtx)

	log.Printf("eami-api: listening on :%d", cfg.Server.Port)
	if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("eami-api: listen: %v", err)
	}
	<-done
	log.Println("eami-api: stopped")
}
