package virtualinputs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onkernel/kernel-images/server/lib/logger"
	"github.com/onkernel/kernel-images/server/lib/scaletozero"
)

const (
	defaultWidth     = 1280
	defaultHeight    = 720
	defaultFrameRate = 30

	stateIdle    = "idle"
	stateRunning = "running"
	statePaused  = "paused"
)

var (
	ErrMissingSources    = errors.New("either video or audio source must be provided")
	ErrVideoURLRequired  = errors.New("video URL must be provided when video source is set")
	ErrAudioURLRequired  = errors.New("audio URL must be provided when audio source is set")
	ErrVideoTypeRequired = errors.New("video source type is required")
	ErrAudioTypeRequired = errors.New("audio source type is required")

	ErrPauseWithoutSession = errors.New("no active virtual input session to pause")
	ErrNoConfigToPause     = errors.New("no previous configuration to pause")
	ErrNoConfigToResume    = errors.New("no virtual input configuration to resume")
)

// SourceType enumerates supported input types.
type SourceType string

const (
	SourceTypeStream SourceType = "stream"
	SourceTypeFile   SourceType = "file"
)

// MediaSource represents a single audio or video input definition.
type MediaSource struct {
	Type SourceType
	URL  string
	Loop bool
}

// Config describes the desired virtual input pipeline.
type Config struct {
	Video     *MediaSource
	Audio     *MediaSource
	Width     int
	Height    int
	FrameRate int
}

// Status reports the current pipeline state.
type Status struct {
	State            string
	VideoDevice      string
	AudioSink        string
	MicrophoneSource string
	Video            *MediaSource
	Audio            *MediaSource
	Width            int
	Height           int
	FrameRate        int
	StartedAt        *time.Time
	LastError        string
}

// Manager coordinates FFmpeg pipelines that feed virtual camera and microphone devices.
type Manager struct {
	mu sync.Mutex

	ffmpegPath       string
	videoDevice      string
	audioSink        string
	microphoneSource string

	defaultWidth     int
	defaultHeight    int
	defaultFrameRate int

	cmd       *exec.Cmd
	exited    chan struct{}
	lastError string
	lastCfg   *Config
	state     string
	startedAt *time.Time

	stz           *scaletozero.Oncer
	scaleDisabled bool
	execCommand   func(name string, arg ...string) *exec.Cmd
}

// NewManager builds a Manager with sensible defaults and optional overrides.
func NewManager(ffmpegPath, videoDevice, audioSink, microphoneSource string, width, height, frameRate int, stz scaletozero.Controller) *Manager {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	if videoDevice == "" {
		videoDevice = "/dev/video20"
	}
	if audioSink == "" {
		audioSink = "audio_input"
	}
	if microphoneSource == "" {
		microphoneSource = "microphone"
	}
	if width <= 0 {
		width = defaultWidth
	}
	if height <= 0 {
		height = defaultHeight
	}
	if frameRate <= 0 {
		frameRate = defaultFrameRate
	}

	return &Manager{
		ffmpegPath:       ffmpegPath,
		videoDevice:      videoDevice,
		audioSink:        audioSink,
		microphoneSource: microphoneSource,
		defaultWidth:     width,
		defaultHeight:    height,
		defaultFrameRate: frameRate,
		state:            stateIdle,
		stz:              scaletozero.NewOncer(stz),
		execCommand:      exec.Command,
	}
}

// Configure starts (or restarts) the pipeline with the provided sources.
// When startPaused is true, silence/black frames are pushed instead of the real inputs.
func (m *Manager) Configure(ctx context.Context, cfg Config, startPaused bool) (Status, error) {
	log := logger.FromContext(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()

	normalized, err := m.normalizeConfig(cfg)
	if err != nil {
		return m.statusLocked(), err
	}

	if err := m.stopLocked(ctx); err != nil {
		return m.statusLocked(), err
	}

	if err := m.ensureVideoDevice(ctx); err != nil {
		return m.statusLocked(), err
	}
	if err := m.ensurePulseDevices(ctx); err != nil {
		return m.statusLocked(), err
	}

	args, err := m.buildFFmpegArgs(normalized, startPaused)
	if err != nil {
		return m.statusLocked(), err
	}

	if err := m.startFFmpegLocked(ctx, args); err != nil {
		return m.statusLocked(), err
	}

	log.Info("virtual inputs started", "state", func() string {
		if startPaused {
			return statePaused
		}
		return stateRunning
	}(), "video_device", m.videoDevice, "audio_sink", m.audioSink)

	m.lastCfg = &normalized
	if startPaused {
		m.state = statePaused
	} else {
		m.state = stateRunning
	}
	now := time.Now()
	m.startedAt = &now
	m.lastError = ""
	return m.statusLocked(), nil
}

// Pause replaces active inputs with silence/black while keeping devices alive.
func (m *Manager) Pause(ctx context.Context) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == stateIdle && m.lastCfg == nil {
		return m.statusLocked(), ErrPauseWithoutSession
	}
	if m.state == statePaused {
		return m.statusLocked(), nil
	}
	if m.lastCfg == nil {
		return m.statusLocked(), ErrNoConfigToPause
	}

	if err := m.stopLocked(ctx); err != nil {
		return m.statusLocked(), err
	}
	args, err := m.buildFFmpegArgs(*m.lastCfg, true)
	if err != nil {
		return m.statusLocked(), err
	}
	if err := m.startFFmpegLocked(ctx, args); err != nil {
		return m.statusLocked(), err
	}
	now := time.Now()
	m.startedAt = &now
	m.state = statePaused
	m.lastError = ""
	return m.statusLocked(), nil
}

// Resume restarts the last configuration with live inputs.
func (m *Manager) Resume(ctx context.Context) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastCfg == nil {
		return m.statusLocked(), ErrNoConfigToResume
	}
	if m.state == stateRunning {
		return m.statusLocked(), nil
	}

	if err := m.stopLocked(ctx); err != nil {
		return m.statusLocked(), err
	}
	args, err := m.buildFFmpegArgs(*m.lastCfg, false)
	if err != nil {
		return m.statusLocked(), err
	}
	if err := m.startFFmpegLocked(ctx, args); err != nil {
		return m.statusLocked(), err
	}
	now := time.Now()
	m.startedAt = &now
	m.state = stateRunning
	m.lastError = ""
	return m.statusLocked(), nil
}

// Stop terminates any running pipeline and clears state.
func (m *Manager) Stop(ctx context.Context) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.stopLocked(ctx); err != nil {
		return m.statusLocked(), err
	}
	m.state = stateIdle
	m.startedAt = nil
	m.lastError = ""
	m.lastCfg = nil
	return m.statusLocked(), nil
}

// Status returns the current status snapshot.
func (m *Manager) Status(_ context.Context) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked()
}

func (m *Manager) statusLocked() Status {
	status := Status{
		State:            m.state,
		VideoDevice:      m.videoDevice,
		AudioSink:        m.audioSink,
		MicrophoneSource: m.microphoneSource,
		LastError:        m.lastError,
		Width:            m.defaultWidth,
		Height:           m.defaultHeight,
		FrameRate:        m.defaultFrameRate,
		StartedAt:        m.startedAt,
	}
	if m.lastCfg != nil {
		status.Width = m.lastCfg.Width
		status.Height = m.lastCfg.Height
		status.FrameRate = m.lastCfg.FrameRate
		status.Video = cloneSource(m.lastCfg.Video)
		status.Audio = cloneSource(m.lastCfg.Audio)
	}
	return status
}

func (m *Manager) normalizeConfig(cfg Config) (Config, error) {
	if cfg.Video == nil && cfg.Audio == nil {
		return Config{}, ErrMissingSources
	}
	if cfg.Video != nil && cfg.Video.URL == "" {
		return Config{}, ErrVideoURLRequired
	}
	if cfg.Audio != nil && cfg.Audio.URL == "" {
		return Config{}, ErrAudioURLRequired
	}
	if cfg.Video != nil && cfg.Video.Type == "" {
		return Config{}, ErrVideoTypeRequired
	}
	if cfg.Audio != nil && cfg.Audio.Type == "" {
		return Config{}, ErrAudioTypeRequired
	}

	out := cfg
	if out.Width <= 0 {
		out.Width = m.defaultWidth
	}
	if out.Height <= 0 {
		out.Height = m.defaultHeight
	}
	if out.FrameRate <= 0 {
		out.FrameRate = m.defaultFrameRate
	}
	return out, nil
}

func (m *Manager) stopLocked(ctx context.Context) error {
	if m.cmd == nil {
		return nil
	}
	defer m.enableScaleToZero(ctx)

	pgid, err := syscall.Getpgid(m.cmd.Process.Pid)
	if err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
	} else {
		_ = m.cmd.Process.Signal(syscall.SIGTERM)
	}

	done := make(chan struct{})
	go func() {
		if m.exited != nil {
			<-m.exited
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		if err == nil {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			_ = m.cmd.Process.Kill()
		}
	}

	m.cmd = nil
	m.exited = nil
	m.state = stateIdle
	return nil
}

func (m *Manager) ensureVideoDevice(ctx context.Context) error {
	if _, err := os.Stat(m.videoDevice); err == nil {
		return nil
	}

	videoNr, err := parseVideoNumber(m.videoDevice)
	if err != nil {
		return fmt.Errorf("invalid video device path: %w", err)
	}

	args := []string{
		"v4l2loopback",
		fmt.Sprintf("video_nr=%d", videoNr),
		"card_label=Virtual Camera",
		"exclusive_caps=1",
	}
	cmd := m.execCommand("modprobe", args...)
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &buf)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to load v4l2loopback: %w: %s", err, strings.TrimSpace(buf.String()))
	}

	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		if _, err := os.Stat(m.videoDevice); err == nil {
			_ = os.Chmod(m.videoDevice, 0o666)
			return nil
		}
		select {
		case <-waitCtx.Done():
			return errors.New("v4l2loopback device did not appear after modprobe")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (m *Manager) ensurePulseDevices(ctx context.Context) error {
	pulseEnv := append(os.Environ(), fmt.Sprintf("PULSE_SERVER=%s", os.Getenv("PULSE_SERVER")))

	check := func(kind, name string) error {
		listCmd := m.execCommand("pactl", "list", "short", kind)
		listCmd.Env = pulseEnv
		out, err := listCmd.Output()
		if err != nil {
			return fmt.Errorf("failed to query pulseaudio %s: %w", kind, err)
		}
		if !strings.Contains(string(out), name) {
			return fmt.Errorf("pulseaudio %s %s not found", kind[:len(kind)-1], name)
		}
		return nil
	}

	for _, checkCase := range []struct {
		kind string
		name string
	}{
		{"sinks", m.audioSink},
		{"sources", m.microphoneSource},
	} {
		if err := check(checkCase.kind, checkCase.name); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) buildFFmpegArgs(cfg Config, paused bool) ([]string, error) {
	var (
		args     []string
		videoIdx = -1
		audioIdx = -1
	)
	args = append(args, "-hide_banner", "-loglevel", "warning", "-nostdin", "-fflags", "+genpts", "-threads", "2")

	// Build inputs and track indexes for mapping.
	if paused {
		if cfg.Video != nil {
			videoIdx = 0
			args = append(args,
				"-f", "lavfi", "-re", "-i", fmt.Sprintf("color=size=%dx%d:rate=%d:color=black", cfg.Width, cfg.Height, cfg.FrameRate),
			)
		}
		if cfg.Audio != nil {
			if videoIdx == -1 {
				audioIdx = 0
			} else {
				audioIdx = 1
			}
			args = append(args, "-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000")
		}
	} else {
		shared := sourcesShared(cfg)
		if cfg.Video != nil {
			videoIdx = 0
			args = append(args, buildInputArgs(cfg.Video)...)
		}
		if cfg.Audio != nil {
			if shared && cfg.Video != nil {
				audioIdx = videoIdx
			} else {
				if videoIdx == -1 {
					audioIdx = 0
				} else {
					audioIdx = 1
				}
				args = append(args, buildInputArgs(cfg.Audio)...)
			}
		}
	}

	if cfg.Video != nil && videoIdx == -1 {
		return nil, errors.New("video mapping requested without input")
	}
	if cfg.Audio != nil && audioIdx == -1 {
		return nil, errors.New("audio mapping requested without input")
	}

	if cfg.Video != nil {
		args = append(args,
			"-map", fmt.Sprintf("%d:v:0", videoIdx),
			"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", cfg.Width, cfg.Height, cfg.Width, cfg.Height),
			"-pix_fmt", "yuv420p",
			"-r", strconv.Itoa(cfg.FrameRate),
			"-f", "v4l2",
			m.videoDevice,
		)
	}

	if cfg.Audio != nil {
		args = append(args,
			"-map", fmt.Sprintf("%d:a:0", audioIdx),
			"-ac", "2",
			"-ar", "48000",
			"-f", "pulse",
			m.audioSink,
		)
	}

	return args, nil
}

func buildInputArgs(src *MediaSource) []string {
	var parts []string
	if src == nil {
		return parts
	}
	if src.Type == SourceTypeFile && src.Loop {
		parts = append(parts, "-stream_loop", "-1")
	}
	if src.Type == SourceTypeStream {
		parts = append(parts, "-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "2")
	}
	parts = append(parts, "-thread_queue_size", "64")
	if src.Type == SourceTypeFile {
		parts = append(parts, "-re")
	}
	parts = append(parts, "-i", src.URL)
	return parts
}

func sourcesShared(cfg Config) bool {
	if cfg.Video == nil || cfg.Audio == nil {
		return false
	}
	return cfg.Video.URL == cfg.Audio.URL && cfg.Video.Type == cfg.Audio.Type && (!cfg.Video.Loop || cfg.Audio.Loop == cfg.Video.Loop)
}

func (m *Manager) startFFmpegLocked(ctx context.Context, args []string) error {
	if err := m.stz.Disable(ctx); err != nil {
		return fmt.Errorf("failed to disable scale-to-zero: %w", err)
	}
	m.scaleDisabled = true

	cmd := m.execCommand(m.ffmpegPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &buf)
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		m.enableScaleToZero(ctx)
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	exited := make(chan struct{})
	m.cmd = cmd
	m.exited = exited

	go func() {
		err := cmd.Wait()
		m.mu.Lock()
		defer m.mu.Unlock()
		if err != nil && m.state != stateIdle {
			m.lastError = fmt.Sprintf("ffmpeg exited: %v: %s", err, strings.TrimSpace(buf.String()))
			m.state = stateIdle
			m.startedAt = nil
		}
		m.cmd = nil
		close(exited)
		m.enableScaleToZero(context.Background())
	}()

	select {
	case <-time.After(300 * time.Millisecond):
		return nil
	case <-exited:
		m.enableScaleToZero(ctx)
		return fmt.Errorf("ffmpeg exited immediately: %s", strings.TrimSpace(buf.String()))
	}
}

func (m *Manager) enableScaleToZero(ctx context.Context) {
	if m.scaleDisabled {
		_ = m.stz.Enable(context.WithoutCancel(ctx))
		m.scaleDisabled = false
	}
}

func parseVideoNumber(path string) (int, error) {
	base := filepath.Base(path)
	num := strings.TrimPrefix(base, "video")
	return strconv.Atoi(num)
}

func cloneSource(src *MediaSource) *MediaSource {
	if src == nil {
		return nil
	}
	copy := *src
	return &copy
}
