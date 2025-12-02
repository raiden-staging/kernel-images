package mediastreamer

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

var (
	ErrAlreadyStreaming = errors.New("a stream is already active")
	ErrNotStreaming     = errors.New("no active stream")
	ErrCannotPause      = errors.New("cannot pause live streams")
)

const (
	defaultVideoDevice = "/dev/video10"
	pulseAudioSink     = "audio_input"
)

// FFmpegStreamer implements MediaStreamer using ffmpeg
type FFmpegStreamer struct {
	mu        sync.RWMutex
	cmd       *exec.Cmd
	config    StreamConfig
	state     StreamState
	startTime time.Time
	lastError string
	cancel    context.CancelFunc
}

// NewFFmpegStreamer creates a new FFmpeg-based media streamer
func NewFFmpegStreamer() *FFmpegStreamer {
	return &FFmpegStreamer{
		state: StateStopped,
	}
}

// Start begins streaming from the configured source
func (s *FFmpegStreamer) Start(ctx context.Context, config StreamConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state != StateStopped {
		return ErrAlreadyStreaming
	}

	// Set default video device if not specified
	if config.VideoDevice == "" {
		config.VideoDevice = defaultVideoDevice
	}

	// Build ffmpeg command based on media type
	args, err := s.buildFFmpegArgs(config)
	if err != nil {
		return fmt.Errorf("failed to build ffmpeg command: %w", err)
	}

	// Create a cancellable context for the ffmpeg process
	cmdCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	s.cmd = exec.CommandContext(cmdCtx, "ffmpeg", args...)

	// Start the ffmpeg process
	if err := s.cmd.Start(); err != nil {
		cancel()
		s.lastError = err.Error()
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	s.config = config
	s.state = StatePlaying
	s.startTime = time.Now()
	s.lastError = ""

	// Monitor the process in a goroutine
	go s.monitorProcess()

	return nil
}

// Stop halts the current stream
func (s *FFmpegStreamer) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state == StateStopped {
		return nil
	}

	if s.cancel != nil {
		s.cancel()
	}

	if s.cmd != nil && s.cmd.Process != nil {
		// Send SIGTERM to allow graceful shutdown
		_ = s.cmd.Process.Signal(nil) // Check if process exists
		_ = s.cmd.Process.Kill()      // Force kill if needed
	}

	s.state = StateStopped
	s.cmd = nil
	s.cancel = nil

	return nil
}

// Pause pauses the current stream (only works for file-based inputs)
func (s *FFmpegStreamer) Pause(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state != StatePlaying {
		return ErrNotStreaming
	}

	// For now, we don't support true pause/resume with ffmpeg
	// This would require more complex process control
	return ErrCannotPause
}

// Resume resumes a paused stream
func (s *FFmpegStreamer) Resume(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state != StatePaused {
		return fmt.Errorf("stream is not paused")
	}

	// This would be implemented with proper pause/resume support
	return ErrCannotPause
}

// Status returns the current streaming status
func (s *FFmpegStreamer) Status(ctx context.Context) StreamStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return StreamStatus{
		Active:    s.state != StateStopped,
		State:     s.state,
		URL:       s.config.URL,
		MediaType: s.config.MediaType,
		Loop:      s.config.Loop,
		StartedAt: s.startTime,
		Error:     s.lastError,
	}
}

// IsActive returns true if a stream is currently active
func (s *FFmpegStreamer) IsActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state != StateStopped
}

// monitorProcess watches the ffmpeg process and updates state
func (s *FFmpegStreamer) monitorProcess() {
	if s.cmd == nil {
		return
	}

	err := s.cmd.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil && s.state == StatePlaying {
		s.lastError = fmt.Sprintf("ffmpeg process exited with error: %v", err)
	}

	s.state = StateStopped
	s.cmd = nil
	s.cancel = nil
}

// buildFFmpegArgs constructs the ffmpeg command arguments based on configuration
func (s *FFmpegStreamer) buildFFmpegArgs(config StreamConfig) ([]string, error) {
	args := []string{
		"-re", // Read input at native frame rate
	}

	// Add loop flag if needed (only works for files, not live streams)
	if config.Loop {
		args = append(args, "-stream_loop", "-1")
	}

	// Input source
	args = append(args, "-i", config.URL)

	// Configure outputs based on media type
	switch config.MediaType {
	case MediaTypeVideo:
		// Video only to v4l2loopback device
		args = append(args,
			"-f", "v4l2",
			"-pix_fmt", "yuv420p",
			"-vcodec", "rawvideo",
			config.VideoDevice,
		)

	case MediaTypeAudio:
		// Audio only to PulseAudio
		args = append(args,
			"-f", "pulse",
			"-ac", "2",              // 2 channels (stereo)
			"-ar", "48000",          // Sample rate
			pulseAudioSink,
		)

	case MediaTypeBoth:
		// Both video and audio - use map to split streams
		// Video output to v4l2loopback
		args = append(args,
			"-map", "0:v:0", // Map first video stream
			"-f", "v4l2",
			"-pix_fmt", "yuv420p",
			"-vcodec", "rawvideo",
			config.VideoDevice,
			"-map", "0:a:0", // Map first audio stream
			"-f", "pulse",
			"-ac", "2",
			"-ar", "48000",
			pulseAudioSink,
		)

	default:
		return nil, fmt.Errorf("invalid media type: %s", config.MediaType)
	}

	return args, nil
}
