package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-test-task/internal/config"
	"go-test-task/internal/migrate"
	"go-test-task/internal/postgres"
	"go-test-task/internal/wallet"
	"go-test-task/internal/wallet/handler"
	walletpg "go-test-task/internal/wallet/postgres"
)

const (
	configPath      = ".env"
	requestTimeout  = 30 * time.Second
	shutdownTimeout = 15 * time.Second
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := listenAndShutdown(); err != nil {
		slog.Error("stopped", "err", err)
		os.Exit(1)
	}
}

func listenAndShutdown() error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	pool, err := postgres.Open(context.Background(), cfg.PostgresDSN(), postgres.Config{
		MaxConns: cfg.DBMaxConns,
		MinConns: cfg.DBMinConns,
	})
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := migrate.Apply(context.Background(), pool); err != nil {
		return err
	}

	router := http.NewServeMux()
	router.HandleFunc("GET /health", handleHealthCheck)
	handler.New(wallet.NewService(walletpg.NewRepository(pool))).Register(router)

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           logRequests(recoverPanics(withRequestTimeout(requestTimeout, router))),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	listenAndServeErr := make(chan error, 1)
	go func() {
		listenAndServeErr <- server.ListenAndServe()
	}()
	slog.Info("listening", "addr", cfg.HTTPAddr)

	shutdownSignal, stopListeningForSignal := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopListeningForSignal()

	select {
	case serveErr := <-listenAndServeErr:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", serveErr)
		}
		return nil
	case <-shutdownSignal.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil {
			return fmt.Errorf("shutdown http server: %w", shutdownErr)
		}
		serveErr := <-listenAndServeErr
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", serveErr)
		}
		return nil
	}
}

func handleHealthCheck(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("ok\n")); err != nil {
		slog.Error("write health response", "err", err)
	}
}
