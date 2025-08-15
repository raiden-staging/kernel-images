package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/onkernel/kernel-images/server/cmd/api/api"
	"github.com/onkernel/kernel-images/server/cmd/config"
	"github.com/onkernel/kernel-images/server/lib/logger"
	"github.com/onkernel/kernel-images/server/lib/recorder"
	"github.com/onkernel/kernel-images/server/lib/scaletozero"
)

func main() {
	slogger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Load configuration from environment variables
	cfg, err := config.Load()
	if err != nil {
		slogger.Error("failed to load configuration", "err", err)
		os.Exit(1)
	}
	slogger.Info("server configuration", "config", cfg)

	// context cancellation on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ensure ffmpeg is available (no-op if not used)
	mustFFmpeg()

	// Router with logger in context
	r := chi.NewRouter()
	r.Use(
		chiMiddleware.Logger,
		chiMiddleware.Recoverer,
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctxWithLogger := logger.AddToContext(r.Context(), slogger)
				next.ServeHTTP(w, r.WithContext(ctxWithLogger))
			})
		},
	)

	// Construct service (manual mode; recorder factory is created but not required for /clipboard)
	defaultParams := recorder.FFmpegRecordingParams{
		DisplayNum:  &cfg.DisplayNum,
		FrameRate:   &cfg.FrameRate,
		MaxSizeInMB: &cfg.MaxSizeInMB,
		OutputDir:   &cfg.OutputDir,
	}
	stz := scaletozero.NewUnikraftCloudController()

	svc, err := api.New(
		recorder.NewFFmpegManager(),
		recorder.NewFFmpegRecorderFactory(cfg.PathToFFmpeg, defaultParams, stz),
	)
	if err != nil {
		slogger.Error("failed to create api service", "err", err)
		os.Exit(1)
	}

	// Manual routes (no oapi)
	r.Get("/health", svc.GetHealthHandler)
	r.Get("/clipboard", svc.GetClipboardHandler)
	r.Post("/clipboard", svc.SetClipboardHandler)
	r.Get("/clipboard/stream", svc.StreamClipboardHandler)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: r,
	}

	go func() {
		slogger.Info("http server starting", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slogger.Error("http server failed", "err", err)
			stop()
		}
	}()

	// graceful shutdown
	<-ctx.Done()
	slogger.Info("shutdown signal received")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	_ = srv.Shutdown(shutdownCtx)
	_ = svc.Shutdown(shutdownCtx)
}

func mustFFmpeg() {
	cmd := exec.Command("ffmpeg", "-version")
	_ = cmd.Run()
}
