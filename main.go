package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/v2"
)

type ServerConfig struct {
	Listen string `koanf:"listen"`
}

type IRISConfig struct {
	URL           string `koanf:"url"`
	APIKey        string `koanf:"api_key"`
	SkipTLSVerify bool   `koanf:"skip_tls_verify"`
}

type DBConfig struct {
	Path string `koanf:"path"`
}

type AlertConfig struct {
	Source           string         `koanf:"source"`
	CustomerID       int            `koanf:"customer_id"`
	ClassificationID int            `koanf:"classification_id"`
	StatusIDNew      int            `koanf:"status_id_new"`
	StatusIDResolved int            `koanf:"status_id_resolved"`
	ResolvedAction   string         `koanf:"resolved_action"`
	DefaultSeverityID int           `koanf:"default_severity_id"`
	SeverityMap      map[string]int `koanf:"severity_map"`
}

type Config struct {
	Server ServerConfig `koanf:"server"`
	IRIS   IRISConfig   `koanf:"iris"`
	DB     DBConfig     `koanf:"db"`
	Alerts AlertConfig  `koanf:"alerts"`
}

func main() {
	k := koanf.New(".")

	// Defaults.
	k.Load(confmap.Provider(map[string]any{
		"server.listen":             ":8080",
		"db.path":                   "./data/badger",
		"alerts.source":             "alertmanager",
		"alerts.customer_id":        1,
		"alerts.status_id_new":      2,
		"alerts.status_id_resolved": 6,
		"alerts.resolved_action":    "update",
		"alerts.default_severity_id": 4,
	}, "."), nil)

	// TOML config file.
	configPath := "config.toml"
	if p := os.Getenv("ALERTIRIS_CONFIG"); p != "" {
		configPath = p
	}
	if err := k.Load(file.Provider(configPath), toml.Parser()); err != nil {
		slog.Warn("could not load config file, using defaults", "path", configPath, "error", err)
	}

	// Env vars: ALERTIRIS_SERVER__LISTEN -> server.listen
	k.Load(env.Provider("ALERTIRIS_", ".", func(s string) string {
		return strings.Replace(
			strings.ToLower(strings.TrimPrefix(s, "ALERTIRIS_")),
			"__", ".", -1,
		)
	}), nil)

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		slog.Error("failed to unmarshal config", "error", err)
		os.Exit(1)
	}

	// BadgerDB.
	opts := badger.DefaultOptions(cfg.DB.Path).WithLogger(nil)
	db, err := badger.Open(opts)
	if err != nil {
		slog.Error("failed to open badger db", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	irisClient := NewIRISClient(cfg.IRIS, cfg.Alerts.CustomerID)
	handler := NewHandler(irisClient, db, cfg.Alerts)

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", handler.HandleWebhook)

	srv := &http.Server{
		Addr:    cfg.Server.Listen,
		Handler: mux,
	}

	go func() {
		slog.Info("starting server", "listen", cfg.Server.Listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down server")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}
}
