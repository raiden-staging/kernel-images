package mediastreamer

import (
	"context"
	"time"
)

// MediaType represents the type of media to stream
type MediaType string

const (
	MediaTypeVideo MediaType = "video"
	MediaTypeAudio MediaType = "audio"
	MediaTypeBoth  MediaType = "both"
)

// StreamState represents the current state of the stream
type StreamState string

const (
	StateStopped StreamState = "stopped"
	StatePlaying StreamState = "playing"
	StatePaused  StreamState = "paused"
)

// StreamConfig contains configuration for a media stream
type StreamConfig struct {
	URL         string
	MediaType   MediaType
	Loop        bool
	VideoDevice string // e.g., /dev/video10
}

// StreamStatus represents the current status of a media stream
type StreamStatus struct {
	Active    bool
	State     StreamState
	URL       string
	MediaType MediaType
	Loop      bool
	StartedAt time.Time
	Error     string
}

// MediaStreamer defines the interface for streaming media to virtual input devices
type MediaStreamer interface {
	// Start begins streaming from the configured source to virtual devices
	Start(ctx context.Context, config StreamConfig) error

	// Stop halts the current stream
	Stop(ctx context.Context) error

	// Pause pauses the current stream (only works for file-based inputs)
	Pause(ctx context.Context) error

	// Resume resumes a paused stream
	Resume(ctx context.Context) error

	// Status returns the current streaming status
	Status(ctx context.Context) StreamStatus

	// IsActive returns true if a stream is currently active
	IsActive() bool
}
