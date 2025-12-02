package mediainput

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onkernel/kernel-images/server/lib/logger"
)

const (
	exitCodeInitValue           = math.MinInt
	exitCodeProcessDoneMinValue = -1
)

// FFmpegMediaInput implements MediaInput using FFmpeg to process media streams
type FFmpegMediaInput struct {
	mu sync.Mutex

	id             string
	ffmpegPath     string
	sourceURL      string
	sourceType     SourceType
	mediaType      MediaType
	loop           bool
	volume         float64
	videoDevice    string // e.g., "/dev/video20" for v4l2loopback
	audioSink      string // PulseAudio sink name
	cmd            *exec.Cmd
	state          PlaybackState
	startTime      time.Time
	exitCode       int
	exited         chan struct{}
	lastError      error
	pulseModuleID  string // ID of the loaded PulseAudio module
}

// FFmpegMediaInputParams contains parameters for creating a media input
type FFmpegMediaInputParams struct {
	SourceURL   string
	SourceType  SourceType
	MediaType   MediaType
	Loop        bool
	Volume      float64
	VideoDevice string // e.g., "/dev/video20"
	AudioSink   string // e.g., "audio_input"
}

func (p FFmpegMediaInputParams) Validate() error {
	if p.SourceURL == "" {
		return fmt.Errorf("source URL is required")
	}
	if p.MediaType == "" {
		return fmt.Errorf("media type is required")
	}
	if p.Volume < 0 || p.Volume > 1 {
		return fmt.Errorf("volume must be between 0.0 and 1.0")
	}
	if p.MediaType == MediaTypeVideo || p.MediaType == MediaTypeBoth {
		if p.VideoDevice == "" {
			return fmt.Errorf("video device is required for video media")
		}
	}
	if p.MediaType == MediaTypeAudio || p.MediaType == MediaTypeBoth {
		if p.AudioSink == "" {
			return fmt.Errorf("audio sink is required for audio media")
		}
	}
	return nil
}

// NewFFmpegMediaInput creates a new FFmpeg-based media input
func NewFFmpegMediaInput(id string, ffmpegPath string, params FFmpegMediaInputParams) (*FFmpegMediaInput, error) {
	if err := params.Validate(); err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}

	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}

	return &FFmpegMediaInput{
		id:          id,
		ffmpegPath:  ffmpegPath,
		sourceURL:   params.SourceURL,
		sourceType:  params.SourceType,
		mediaType:   params.MediaType,
		loop:        params.Loop,
		volume:      params.Volume,
		videoDevice: params.VideoDevice,
		audioSink:   params.AudioSink,
		state:       StateStopped,
		exitCode:    exitCodeInitValue,
	}, nil
}

func (m *FFmpegMediaInput) ID() string {
	return m.id
}

func (m *FFmpegMediaInput) State() PlaybackState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *FFmpegMediaInput) Status() *MediaStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	status := &MediaStatus{
		State:      m.state,
		SourceURL:  m.sourceURL,
		SourceType: m.sourceType,
		MediaType:  m.mediaType,
		Loop:       m.loop,
		Volume:     m.volume,
		StartTime:  m.startTime,
	}

	if m.lastError != nil {
		status.Error = m.lastError.Error()
	}

	return status
}

func (m *FFmpegMediaInput) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state == StatePlaying {
		return fmt.Errorf("media input is already playing")
	}

	// Build ffmpeg command
	args, err := m.buildFFmpegArgs()
	if err != nil {
		m.state = StateError
		m.lastError = err
		return fmt.Errorf("failed to build ffmpeg arguments: %w", err)
	}

	logger.Info("Starting media input with ffmpeg: %s %s", m.ffmpegPath, strings.Join(args, " "))

	m.cmd = exec.Command(m.ffmpegPath, args...)
	m.exited = make(chan struct{})
	m.startTime = time.Now()
	m.state = StatePlaying
	m.lastError = nil

	if err := m.cmd.Start(); err != nil {
		m.state = StateError
		m.lastError = err
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Monitor the process in a goroutine
	go m.monitorProcess()

	return nil
}

func (m *FFmpegMediaInput) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state == StateStopped {
		return nil
	}

	if m.cmd != nil && m.cmd.Process != nil {
		// Send SIGTERM for graceful shutdown
		if err := m.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			logger.Warn("Failed to send SIGTERM to ffmpeg process: %v", err)
		}

		// Wait for process to exit (with timeout)
		select {
		case <-m.exited:
			logger.Info("FFmpeg process exited gracefully")
		case <-time.After(5 * time.Second):
			logger.Warn("FFmpeg process did not exit gracefully, killing it")
			if err := m.cmd.Process.Kill(); err != nil {
				logger.Error("Failed to kill ffmpeg process: %v", err)
			}
		}
	}

	m.state = StateStopped
	return nil
}

func (m *FFmpegMediaInput) Pause(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state != StatePlaying {
		return fmt.Errorf("can only pause when playing")
	}

	if m.cmd != nil && m.cmd.Process != nil {
		// Send SIGSTOP to pause the process
		if err := m.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
			m.lastError = err
			return fmt.Errorf("failed to pause ffmpeg process: %w", err)
		}
		m.state = StatePaused
	}

	return nil
}

func (m *FFmpegMediaInput) Resume(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state != StatePaused {
		return fmt.Errorf("can only resume when paused")
	}

	if m.cmd != nil && m.cmd.Process != nil {
		// Send SIGCONT to resume the process
		if err := m.cmd.Process.Signal(syscall.SIGCONT); err != nil {
			m.lastError = err
			return fmt.Errorf("failed to resume ffmpeg process: %w", err)
		}
		m.state = StatePlaying
	}

	return nil
}

func (m *FFmpegMediaInput) UpdateSettings(ctx context.Context, settings MediaSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Update loop setting
	if settings.Loop != nil {
		m.loop = *settings.Loop
		// Note: To apply loop changes, we'd need to restart the process
		// For now, we just update the setting for the next start
	}

	// Update volume setting
	if settings.Volume != nil {
		if *settings.Volume < 0 || *settings.Volume > 1 {
			return fmt.Errorf("volume must be between 0.0 and 1.0")
		}
		m.volume = *settings.Volume
		// TODO: Dynamically update volume using pactl for running streams
	}

	return nil
}

func (m *FFmpegMediaInput) monitorProcess() {
	defer close(m.exited)

	if m.cmd == nil {
		return
	}

	err := m.cmd.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	if err != nil {
		logger.Error("FFmpeg process exited with error: %v", err)
		m.lastError = err
		m.state = StateError
	} else {
		logger.Info("FFmpeg process exited successfully")
		m.state = StateStopped
	}

	if m.cmd.ProcessState != nil {
		m.exitCode = m.cmd.ProcessState.ExitCode()
	}
}

func (m *FFmpegMediaInput) buildFFmpegArgs() ([]string, error) {
	args := []string{
		"-loglevel", "warning",
	}

	// Add loop flag if needed (for file sources)
	if m.loop && m.sourceType == SourceTypeFile {
		args = append(args, "-stream_loop", "-1")
	}

	// Add input source with appropriate format
	args = append(args, "-re") // Read input at native frame rate

	// Add format-specific input options
	switch m.sourceType {
	case SourceTypeHLS:
		args = append(args, "-i", m.sourceURL)
	case SourceTypeRTMP, SourceTypeRTMPS:
		args = append(args, "-i", m.sourceURL)
	case SourceTypeDASH:
		args = append(args, "-i", m.sourceURL)
	case SourceTypeFile, SourceTypeStream:
		args = append(args, "-i", m.sourceURL)
	default:
		args = append(args, "-i", m.sourceURL)
	}

	// Configure outputs based on media type
	switch m.mediaType {
	case MediaTypeAudio:
		args = append(args, m.buildAudioOutputArgs()...)
	case MediaTypeVideo:
		args = append(args, m.buildVideoOutputArgs()...)
	case MediaTypeBoth:
		args = append(args, m.buildAudioOutputArgs()...)
		args = append(args, m.buildVideoOutputArgs()...)
	}

	return args, nil
}

func (m *FFmpegMediaInput) buildAudioOutputArgs() []string {
	args := []string{}

	// Audio encoding for PulseAudio
	args = append(args,
		"-f", "pulse",
		"-device", m.audioSink,
	)

	// Apply volume filter if needed
	if m.volume != 1.0 {
		volumeStr := strconv.FormatFloat(m.volume, 'f', 2, 64)
		args = append(args, "-af", fmt.Sprintf("volume=%s", volumeStr))
	}

	// Use copy if possible, otherwise re-encode
	args = append(args, "-acodec", "pcm_s16le")
	args = append(args, "-ar", "48000")
	args = append(args, "-ac", "2")

	return args
}

func (m *FFmpegMediaInput) buildVideoOutputArgs() []string {
	args := []string{}

	// Video encoding for v4l2loopback
	args = append(args,
		"-f", "v4l2",
		"-pix_fmt", "yuv420p",
		m.videoDevice,
	)

	return args
}
