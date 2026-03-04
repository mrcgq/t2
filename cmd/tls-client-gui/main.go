package main

import (
	"embed"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/pkg/browser"
	"go.uber.org/zap"

	"github.com/user/tls-client-gui/internal/gui"
)

//go:embed ../../web/index.html
var webFS embed.FS

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:19090", "GUI server listen address")
	configPath := flag.String("config", "", "path to config.yaml (optional, can load via GUI)")
	noBrowser := flag.Bool("no-browser", false, "do not auto-open browser")
	showVersion := flag.Bool("version", false, "print version and exit")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	flag.Parse()

	if *showVersion {
		fmt.Printf("tls-client-gui %s (commit=%s, built=%s)\n", version, commit, date)
		os.Exit(0)
	}

	logger, err := newLogger(*logLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger init: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = logger.Sync() }()

	logger.Info("tls-client-gui starting",
		zap.String("version", version),
		zap.String("addr", *addr),
	)

	srv, err := gui.NewServer(gui.ServerConfig{
		Addr:       *addr,
		Logger:     logger,
		WebFS:      webFS,
		ConfigPath: *configPath,
		Version:    version,
		Commit:     commit,
		Date:       date,
	})
	if err != nil {
		logger.Fatal("server init failed", zap.Error(err))
	}

	if err := srv.Start(); err != nil {
		logger.Fatal("server start failed", zap.Error(err))
	}

	url := fmt.Sprintf("http://%s", *addr)
	logger.Info("GUI ready", zap.String("url", url))

	if !*noBrowser {
		if err := browser.OpenURL(url); err != nil {
			logger.Warn("failed to open browser, please open manually",
				zap.String("url", url),
				zap.Error(err),
			)
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	logger.Info("shutting down", zap.String("signal", sig.String()))

	srv.Stop()
}

func newLogger(level string) (*zap.Logger, error) {
	var cfg zap.Config
	switch level {
	case "debug":
		cfg = zap.NewDevelopmentConfig()
	default:
		cfg = zap.NewProductionConfig()
	}
	cfg.OutputPaths = []string{"stderr"}
	switch level {
	case "debug":
		cfg.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "warn":
		cfg.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		cfg.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		cfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}
	return cfg.Build()
}
