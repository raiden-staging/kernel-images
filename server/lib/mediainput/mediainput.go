package mediainput

import (
	"context"
	"time"
)

// MediaType represents the type of media
type MediaType string

const (
	MediaTypeAudio MediaType = "audio"
	MediaTypeVideo MediaType = "video"
	MediaTypeBoth  MediaType = "both"
)

// SourceType represents the type of media source
type SourceType string

const (
	SourceTypeFile   SourceType = "file"
	SourceTypeHLS    SourceType = "hls"
	SourceTypeRTMP   SourceType = "rtmp"
	SourceTypeRTMPS  SourceType = "rtmps"
	SourceTypeDASH   SourceType = "dash"
	SourceTypeStream SourceType = "stream" // Generic streaming source
)

// PlaybackState represents the current state of media playback
type PlaybackState string

const (
	StateStopped PlaybackState = "stopped"
	StatePlaying PlaybackState = "playing"
	StatePaused  PlaybackState = "paused"
	StateError   PlaybackState = "error"
)

// MediaInput defines the interface for media input functionality.
type MediaInput interface {
	ID() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	State() PlaybackState
	Status() *MediaStatus
	UpdateSettings(ctx context.Context, settings MediaSettings) error
}

// MediaStatus represents the current status of a media input
type MediaStatus struct {
	State      PlaybackState
	SourceURL  string
	SourceType SourceType
	MediaType  MediaType
	Loop       bool
	Volume     float64
	StartTime  time.Time
	Error      string
}

// MediaSettings represents configurable settings for media input
type MediaSettings struct {
	Loop   *bool
	Volume *float64 // 0.0 to 1.0
}

// MediaInputManager defines the interface for managing multiple media input instances.
// Implementations should be thread-safe for concurrent access.
type MediaInputManager interface {
	// GetMediaInput retrieves a media input by its ID.
	// Returns the media input and true if found, nil and false otherwise.
	GetMediaInput(id string) (MediaInput, bool)

	// ListActiveInputs returns a list of all active media inputs
	ListActiveInputs(ctx context.Context) []MediaInput

	// RegisterMediaInput registers a media input with the given ID.
	// Returns an error if a media input with the same ID already exists.
	RegisterMediaInput(ctx context.Context, input MediaInput) error

	// DeregisterMediaInput removes a media input from the manager.
	DeregisterMediaInput(ctx context.Context, input MediaInput) error

	// StopAll stops all active media inputs.
	StopAll(ctx context.Context) error
}
