//go:build !oapi

package api

import (
	"context"
	"time"

	"github.com/onkernel/kernel-images/server/lib/recorder"
)

// Minimal ApiService for manual routing (no generated oapi code needed).
type ApiService struct {
	clipboardManager *ClipboardManager
	startTime        time.Time
}

// Keep the constructor signature compatible with the oapi-based version.
// The arguments are accepted to satisfy callers; they are not required for clipboard routes.
func New(_ recorder.RecordManager, _ recorder.FFmpegRecorderFactory) (*ApiService, error) {
	return &ApiService{
		clipboardManager: NewClipboardManager(),
		startTime:        time.Now(),
	}, nil
}

func (s *ApiService) Shutdown(context.Context) error { return nil }
