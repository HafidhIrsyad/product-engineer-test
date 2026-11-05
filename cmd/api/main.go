// Command api is the composition root: it wires config, logging, store, app and api.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/HafidhIrsyad/product-engineer-test/internal/api"
	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/config"
	"github.com/HafidhIrsyad/product-engineer-test/internal/logging"
	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger, err := logging.New(cfg.IsLocal(), cfg.Log.Level)
	if err != nil {
		return fmt.Errorf("logger: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := store.Migrate(ctx, cfg.Database.URL); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	var tracer pgx.QueryTracer
	if cfg.IsLocal() {
		tracer = logging.SQLTracer()
	}
	st, err := store.Open(ctx, cfg.Database.URL, tracer)
	if err != nil {
		return err
	}
	defer st.Close()

	svc := app.NewService(st)
	go svc.RunExpirySweeper(logger.WithContext(ctx), cfg.Sweeper.Interval)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.HTTP.Port),
		Handler:           api.NewRouter(svc, logger, cfg.HTTP),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      cfg.HTTP.RequestTimeout + 5*time.Second, // longer than the request timeout middleware
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	logger.Info().Int("port", cfg.HTTP.Port).Str("env", cfg.App.Env).Msg("server started")

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		logger.Info().Msg("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
