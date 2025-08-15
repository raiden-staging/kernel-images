package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/onkernel/kernel-images/server/cmd/api/api"
	"github.com/onkernel/kernel-images/server/lib/clipboard"
	"github.com/onkernel/kernel-images/server/lib/logger"
	"github.com/onkernel/kernel-images/server/lib/recorder"
)

func main() {
	ctx := context.Background()
	log := logger.FromContext(ctx)

	// Create API service
	recordManager := recorder.NewRecordManager()
	factory := recorder.NewFFmpegRecorderFactory()

	apiService, err := api.New(recordManager, factory)
	if err != nil {
		log.Fatal("Failed to create API service", "err", err)
	}

	// Create clipboard manager
	apiService.SetClipboardManager(api.NewClipboardManager())

	// Create router
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Add routes
	r.Get("/health", apiService.GetHealthHandler)

	// Clipboard routes
	r.Get("/clipboard", apiService.GetClipboardHandler)
	r.Post("/clipboard", apiService.SetClipboardHandler)
	r.Get("/clipboard/stream", apiService.StreamClipboardHandler)

	// Determine port
	port := os.Getenv("PORT")
	if port == "" {
		port = "10001"
	}

	// Start server
	server := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	// Handle graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Info("Shutting down server...")

		// Create shutdown context with timeout
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := apiService.Shutdown(shutdownCtx); err != nil {
			log.Error("Error shutting down API service", "err", err)
		}

		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Error("Error shutting down HTTP server", "err", err)
		}
	}()

	// Start server
	log.Info("Starting server", "port", port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal("Failed to start server", "err", err)
	}
}
