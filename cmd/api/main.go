// Command api is the composition root: it wires config, logging, store, app and api.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/HafidhIrsyad/product-engineer-test/internal/api"
	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/config"
	"github.com/HafidhIrsyad/product-engineer-test/internal/logging"
	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

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

	// The first SIGINT/SIGTERM starts a graceful shutdown. serve calls stop(), which restores the
	// default behaviour, so a second signal kills the process at once.
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
	// Deferred first, so it runs last: the pool closes only after the server and the sweeper are done.
	defer st.Close()

	svc := app.NewService(st)

	// The sweeper has its own context: it is stopped after the server, not by the signal itself.
	sweeperCtx, stopSweeper := context.WithCancel(logger.WithContext(context.Background()))
	var sweeper sync.WaitGroup
	sweeper.Add(1)
	go func() {
		defer sweeper.Done()
		svc.RunExpirySweeper(sweeperCtx, cfg.Sweeper.Interval)
	}()
	defer func() {
		stopSweeper()
		sweeper.Wait()
		logger.Info().Msg("sweeper stopped")
	}()

	srv := &http.Server{
		Handler:           api.NewRouter(svc, logger, cfg.HTTP),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      cfg.HTTP.RequestTimeout + 5*time.Second, // longer than the request timeout middleware
		IdleTimeout:       60 * time.Second,
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.HTTP.Port))
	if err != nil {
		return err
	}
	logger.Info().Int("port", cfg.HTTP.Port).Str("env", cfg.App.Env).Msg("server started")

	return serve(ctx, stop, srv, ln, shutdownPlan{
		drainDelay: cfg.HTTP.DrainDelay, timeout: cfg.HTTP.ShutdownTimeout, drain: svc.Drain, logger: logger,
	})
}

type shutdownPlan struct {
	drainDelay time.Duration // wait between "not ready" and "stop listening"
	timeout    time.Duration // how long running requests may take to finish
	drain      func()        // makes /healthz fail
	logger     zerolog.Logger
}

// serve runs srv until ctx is cancelled or the server fails, then shuts down in order:
//  1. /healthz starts failing, so a load balancer stops sending new requests;
//  2. after drainDelay the server stops accepting connections and waits for running requests;
//  3. if they do not finish within timeout, the remaining connections are closed.
func serve(ctx context.Context, stop func(), srv *http.Server, ln net.Listener, plan shutdownPlan) error {
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		return err // the server died on its own: nothing is left to drain
	case <-ctx.Done():
	}
	stop() // a second signal now terminates the process
	plan.logger.Info().Msg("shutting down: no longer ready")
	plan.drain()
	time.Sleep(plan.drainDelay)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), plan.timeout)
	defer cancel()
	plan.logger.Info().Dur("timeout", plan.timeout).Msg("shutting down: waiting for running requests")
	err := srv.Shutdown(shutdownCtx)
	if err != nil {
		_ = srv.Close() // out of time: cut the connections that are left
		return fmt.Errorf("shutdown did not finish in %s: %w", plan.timeout, err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	plan.logger.Info().Msg("shutting down: server stopped")
	return nil
}
