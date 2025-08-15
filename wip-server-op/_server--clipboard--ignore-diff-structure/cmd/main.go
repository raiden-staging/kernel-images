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

	cfg, err := config.Load()
	if err != nil {
		slogger.Error("failed to load configuration", "err", err)
		os.Exit(1)
	}
	slogger.Info("server configuration", "config", cfg)

	mustFFmpeg()

	// Router with logger in request context
	r := chi.NewRouter()
	r.Use(
		chiMiddleware.Logger,
		chiMiddleware.Recoverer,
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				ctxWithLogger := logger.AddToContext(req.Context(), slogger)
				next.ServeHTTP(w, req.WithContext(ctxWithLogger))
			})
		},
	)

	// Recorder deps (kept minimal; endpoints below only expose clipboard/health)
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

	// Manual HTTP wiring (no oapi codegen required)
	r.Get("/health", svc.GetHealthHandler)
	r.Get("/clipboard", svc.GetClipboardHandler)
	r.Post("/clipboard", svc.SetClipboardHandler)
	r.Get("/clipboard/stream", svc.StreamClipboardHandler)

	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{Addr: addr, Handler: r}

	// Start
	go func() {
		slogger.Info("http server starting", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slogger.Error("http server failed", "err", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	<-ctx.Done()
	stop()
	slogger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := svc.Shutdown(shutdownCtx); err != nil {
		slogger.Error("api service shutdown error", "err", err)
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slogger.Error("http server shutdown error", "err", err)
	}
}

func mustFFmpeg() {
	cmd := exec.Command("ffmpeg", "-version")
	_ = cmd.Run() // clipboard/health don’t require ffmpeg; ignore absence
}
