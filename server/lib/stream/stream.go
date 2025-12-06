package stream

import (
	"context"
	"time"
)

type Mode string

const (
	ModeInternal Mode = "internal"
	ModeRemote   Mode = "remote"
	ModeWebRTC   Mode = "webrtc"
	ModeSocket   Mode = "socket"
)

// Params holds stream creation settings.
type Params struct {
	FrameRate         *int
	DisplayNum        *int
	IngestURL         string
	Mode              Mode
	PlaybackURL       *string
	SecurePlaybackURL *string
}

// Metadata describes a running stream.
type Metadata struct {
	ID                string
	Mode              Mode
	IngestURL         string
	PlaybackURL       *string
	SecurePlaybackURL *string
	StartedAt         time.Time
	WebsocketURL      *string
	WebRTCOfferURL    *string
}

// Streamer defines the interface for a streaming session.
type Streamer interface {
	ID() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	IsStreaming(ctx context.Context) bool
	Metadata() Metadata
}

// WebSocketEndpoint allows clients to register for chunked playback.
type WebSocketEndpoint interface {
	Streamer
	RegisterClient(conn WebSocketConn) error
}

// WebSocketConn mirrors the subset of websocket.Conn needed by streamers to remain decoupled.
type WebSocketConn interface {
	Read(ctx context.Context) (messageType int, p []byte, err error)
	Write(ctx context.Context, messageType int, data []byte) error
	Close(status int, reason string) error
}

// WebRTCNegotiator supports SDP offer/answer exchange for live streaming.
type WebRTCNegotiator interface {
	Streamer
	HandleOffer(ctx context.Context, offer string) (string, error)
}

// Manager defines the interface for tracking streaming sessions.
type Manager interface {
	GetStream(id string) (Streamer, bool)
	ListStreams(ctx context.Context) []Streamer
	RegisterStream(ctx context.Context, streamer Streamer) error
	DeregisterStream(ctx context.Context, streamer Streamer) error
	StopAll(ctx context.Context) error
}

// FFmpegStreamerFactory returns a Streamer configured with the provided id and params.
type FFmpegStreamerFactory func(id string, params Params) (Streamer, error)

// InternalServer represents the internal RTMP(S) server used for live streaming.
type InternalServer interface {
	Start(ctx context.Context) error
	EnsureStream(path string)
	IngestURL(streamPath string) string
	PlaybackURLs(host string, streamPath string) (rtmpURL *string, rtmpsURL *string)
	Close(ctx context.Context) error
}
