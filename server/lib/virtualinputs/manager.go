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

	modeDevice      = "device"
	modeVirtualFile = "virtual-file"

	defaultVideoFile = "/tmp/virtual-inputs/video.y4m"
	defaultAudioFile = "/tmp/virtual-inputs/audio.wav"
	defaultVideoPipe = "/tmp/virtual-inputs/ingest-video.pipe"
	defaultAudioPipe = "/tmp/virtual-inputs/ingest-audio.pipe"

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
	ErrUnsupportedVideo  = errors.New("unsupported video format for realtime ingest")
	ErrUnsupportedAudio  = errors.New("unsupported audio format for realtime ingest")

	ErrPauseWithoutSession = errors.New("no active virtual input session to pause")
	ErrNoConfigToPause     = errors.New("no previous configuration to pause")
	ErrNoConfigToResume    = errors.New("no virtual input configuration to resume")
)

// SourceType enumerates supported input types.
type SourceType string

const (
	SourceTypeStream SourceType = "stream"
	SourceTypeFile   SourceType = "file"
	SourceTypeSocket SourceType = "socket"
	SourceTypeWebRTC SourceType = "webrtc"
)

// MediaSource represents a single audio or video input definition.
type MediaSource struct {
	Type SourceType
	URL  string
	// Format hints the expected container/codec when the source is a socket or WebRTC feed
	// (e.g. "wav" for audio sockets, "mpegts" for video sockets, "ivf"/"ogg" for WebRTC).
	Format string
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
	Mode             string
	VideoFile        string
	AudioFile        string
	Ingest           *IngestStatus
}

// IngestEndpoint describes how callers can push realtime media into the pipelines.
type IngestEndpoint struct {
	Protocol string
	Format   string
	Path     string
}

// IngestStatus surfaces the ingest endpoints for audio/video when socket or WebRTC sources are active.
type IngestStatus struct {
	Video *IngestEndpoint
	Audio *IngestEndpoint
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

	cmd            *exec.Cmd
	exited         chan struct{}
	processGroupID int
	lastError      string
	lastCfg        *Config
	state          string
	mode           string
	startedAt      *time.Time
	videoFile      string
	audioFile      string
	audioPipe      string
	videoPipe      string
	ingest         *IngestStatus
	videoKeepalive *os.File
	audioKeepalive *os.File

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
		mode:             modeDevice,
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

	useVirtualFileMode := false
	if usesRealtimeSource(normalized) {
		useVirtualFileMode = true
	}
	// Avoid accidentally routing injected audio into the playback sink; force the
	// dedicated virtual input sink when the configured sink is the output sink.
	if m.audioSink == "audio_output" {
		log.Warn("forcing virtual input audio sink to avoid leaking into output sink")
		m.audioSink = "audio_input"
	}
	if normalized.Video != nil && normalized.Video.Type != SourceTypeSocket && normalized.Video.Type != SourceTypeWebRTC {
		if ok, err := m.ensureVideoDevice(ctx); err != nil || !ok {
			useVirtualFileMode = true
			log.Warn("v4l2loopback unavailable, using virtual capture files instead", "err", err)
		}
	}
	if err := m.ensurePulseDevices(ctx); err != nil {
		return m.statusLocked(), err
	}

	m.setDefaultPulseDevices(ctx)

	m.killAllFFmpeg()

	if useVirtualFileMode {
		m.mode = modeVirtualFile
		m.audioFile = ""
		m.videoFile = ""
		// Only set video/audio files for non-realtime sources (file/stream).
		// WebSocket and WebRTC sources use the virtual feed page instead of a Y4M file,
		// so we skip setting videoFile to avoid --use-file-for-fake-video-capture.
		if normalized.Video != nil && normalized.Video.Type != SourceTypeSocket && normalized.Video.Type != SourceTypeWebRTC {
			m.videoFile = defaultVideoFile
		}
		if normalized.Audio != nil && normalized.Audio.Type != SourceTypeSocket && normalized.Audio.Type != SourceTypeWebRTC {
			m.audioFile = defaultAudioFile
		}
		captureDir := filepath.Dir(defaultVideoFile)
		if err := os.MkdirAll(captureDir, 0o755); err != nil {
			return m.statusLocked(), fmt.Errorf("prepare virtual capture dir: %w", err)
		}
		if m.videoFile != "" {
			_ = os.Remove(m.videoFile)
		}
		if m.audioFile != "" {
			_ = os.Remove(m.audioFile)
		}
	} else {
		m.mode = modeDevice
		m.videoFile = ""
		m.audioFile = ""
	}

	m.ingest = buildIngestStatus(normalized)
	if needsVideoPipe(normalized) {
		if err := preparePipe(normalized.Video.URL); err != nil {
			return m.statusLocked(), err
		}
		m.videoPipe = normalized.Video.URL
	}
	if needsAudioPipe(normalized) {
		if err := preparePipe(normalized.Audio.URL); err != nil {
			return m.statusLocked(), err
		}
		m.audioPipe = normalized.Audio.URL
	}

	args, err := m.buildFFmpegArgs(normalized, startPaused)
	if err != nil {
		return m.statusLocked(), err
	}

	// Open keepalives BEFORE starting FFmpeg so pipes have readers/writers available.
	// This prevents FFmpeg from blocking or failing when opening the FIFO.
	m.openPipeKeepalivesLocked(ctx, normalized, startPaused)

	if err := m.startFFmpegLocked(ctx, args); err != nil {
		m.closePipeKeepalivesLocked()
		return m.statusLocked(), err
	}

	log.Info("virtual inputs started", "state", func() string {
		if startPaused {
			return statePaused
		}
		return stateRunning
	}(), "video_device", m.videoDevice, "audio_sink", m.audioSink, "mode", m.mode, "video_file", m.videoFile, "audio_file", m.audioFile)

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
	m.killAllFFmpeg()
	m.setDefaultPulseDevices(ctx)
	args, err := m.buildFFmpegArgs(*m.lastCfg, true)
	if err != nil {
		return m.statusLocked(), err
	}
	// Open keepalives before starting FFmpeg
	m.openPipeKeepalivesLocked(ctx, *m.lastCfg, true)
	if err := m.startFFmpegLocked(ctx, args); err != nil {
		m.closePipeKeepalivesLocked()
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
	m.killAllFFmpeg()
	m.setDefaultPulseDevices(ctx)
	args, err := m.buildFFmpegArgs(*m.lastCfg, false)
	if err != nil {
		return m.statusLocked(), err
	}
	// Open keepalives before starting FFmpeg
	m.openPipeKeepalivesLocked(ctx, *m.lastCfg, false)
	if err := m.startFFmpegLocked(ctx, args); err != nil {
		m.closePipeKeepalivesLocked()
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
	if err := m.ensureNoFFmpeg(); err != nil {
		m.lastError = err.Error()
		return m.statusLocked(), err
	}
	m.state = stateIdle
	m.startedAt = nil
	m.lastError = ""
	m.lastCfg = nil
	m.mode = modeDevice
	m.videoFile = ""
	m.audioFile = ""
	m.videoPipe = ""
	m.audioPipe = ""
	m.ingest = nil
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
		Mode:             m.mode,
		VideoFile:        m.videoFile,
		AudioFile:        m.audioFile,
		Ingest:           m.ingest,
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

	if cfg.Video != nil {
		cfg.Video.Type = normalizeSourceType(cfg.Video.Type)
	}
	if cfg.Audio != nil {
		cfg.Audio.Type = normalizeSourceType(cfg.Audio.Type)
	}

	if cfg.Video != nil && cfg.Video.Type == "" {
		return Config{}, ErrVideoTypeRequired
	}
	if cfg.Audio != nil && cfg.Audio.Type == "" {
		return Config{}, ErrAudioTypeRequired
	}
	if cfg.Video != nil && cfg.Video.URL == "" && cfg.Video.Type != SourceTypeSocket && cfg.Video.Type != SourceTypeWebRTC {
		return Config{}, ErrVideoURLRequired
	}
	if cfg.Audio != nil && cfg.Audio.URL == "" && cfg.Audio.Type != SourceTypeSocket && cfg.Audio.Type != SourceTypeWebRTC {
		return Config{}, ErrAudioURLRequired
	}

	out := cfg
	out.Video = normalizeSource(cfg.Video, true)
	out.Audio = normalizeSource(cfg.Audio, false)
	if err := validateRealtimeFormat(out.Video, true); err != nil {
		return Config{}, err
	}
	if err := validateRealtimeFormat(out.Audio, false); err != nil {
		return Config{}, err
	}
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
	m.closePipeKeepalivesLocked()
	if m.cmd == nil {
		if m.processGroupID > 0 {
			m.killAllFFmpeg()
			if !processGroupAlive(m.processGroupID) {
				m.processGroupID = 0
			}
		}
		return nil
	}
	defer m.enableScaleToZero(ctx)

	pid := m.cmd.Process.Pid
	if !processAlive(pid) {
		m.cmd = nil
		m.exited = nil
		m.state = stateIdle
		if !processGroupAlive(m.processGroupID) {
			m.processGroupID = 0
		}
		return nil
	}

	pgid, _ := syscall.Getpgid(m.cmd.Process.Pid)
	if pgid > 0 {
		m.processGroupID = pgid
	}
	killProcessGroupOrPID(pgid, pid, syscall.SIGTERM)

	waitCtx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	waitDone := make(chan struct{})
	go func() {
		if m.exited != nil {
			<-m.exited
		}
		close(waitDone)
	}()

	select {
	case <-waitDone:
	case <-waitCtx.Done():
		killProcessGroupOrPID(pgid, pid, syscall.SIGKILL)
		select {
		case <-waitDone:
		case <-time.After(2 * time.Second):
		}
	}

	if processAlive(pid) || processGroupAlive(m.processGroupID) {
		m.killAllFFmpeg()
	}

	m.cmd = nil
	m.exited = nil
	m.state = stateIdle
	if !processGroupAlive(m.processGroupID) {
		m.processGroupID = 0
	}
	return nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func processGroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	return syscall.Kill(-pgid, 0) == nil
}

func killProcessGroupOrPID(pgid int, pid int, sig syscall.Signal) {
	if pgid > 0 {
		_ = syscall.Kill(-pgid, sig)
		return
	}
	if pid > 0 {
		_ = syscall.Kill(pid, sig)
	}
}

func (m *Manager) killAllFFmpeg() {
	if m.processGroupID > 0 {
		killProcessGroupOrPID(m.processGroupID, 0, syscall.SIGTERM)
	}
	m.killVirtualFFmpegProcesses()
	time.Sleep(150 * time.Millisecond)
	if m.processGroupID > 0 {
		killProcessGroupOrPID(m.processGroupID, 0, syscall.SIGKILL)
	}
	m.killVirtualFFmpegProcesses()
}

func (m *Manager) ensureNoFFmpeg() error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		m.killAllFFmpeg()
		if !m.ownedFFmpegRunning() {
			m.processGroupID = 0
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("virtual input ffmpeg processes still running after stop")
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func (m *Manager) ownedFFmpegRunning() bool {
	if processGroupAlive(m.processGroupID) {
		return true
	}
	procs, err := m.virtualFFmpegProcesses()
	return err == nil && len(procs) > 0
}

type ffmpegProcess struct {
	pid     int
	cmdline string
}

func (m *Manager) virtualFFmpegProcesses() ([]ffmpegProcess, error) {
	cmd := m.execCommand("pgrep", "-a", "ffmpeg")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}

	out := []ffmpegProcess{}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 0 {
			continue
		}
		pid, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		cmdline := ""
		if len(parts) > 1 {
			cmdline = parts[1]
		}
		if m.isVirtualFFmpegCommand(cmdline) {
			out = append(out, ffmpegProcess{pid: pid, cmdline: cmdline})
		}
	}
	return out, nil
}

func (m *Manager) killVirtualFFmpegProcesses() {
	procs, err := m.virtualFFmpegProcesses()
	if err != nil || len(procs) == 0 {
		return
	}
	for _, proc := range procs {
		pgid, _ := syscall.Getpgid(proc.pid)
		killProcessGroupOrPID(pgid, proc.pid, syscall.SIGTERM)
	}
	time.Sleep(100 * time.Millisecond)
	for _, proc := range procs {
		pgid, _ := syscall.Getpgid(proc.pid)
		killProcessGroupOrPID(pgid, proc.pid, syscall.SIGKILL)
	}
}

func (m *Manager) isVirtualFFmpegCommand(cmdline string) bool {
	markers := []string{
		m.videoDevice,
		m.audioSink,
		m.microphoneSource,
		defaultVideoFile,
		defaultAudioFile,
		"/tmp/virtual-inputs",
	}
	for _, marker := range markers {
		if marker != "" && strings.Contains(cmdline, marker) {
			return true
		}
	}
	return false
}

func (m *Manager) ensureVideoDevice(ctx context.Context) (bool, error) {
	if _, err := os.Stat(m.videoDevice); err == nil {
		return true, nil
	}

	videoNr, err := parseVideoNumber(m.videoDevice)
	if err != nil {
		return false, fmt.Errorf("invalid video device path: %w", err)
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
		return false, fmt.Errorf("failed to load v4l2loopback: %w: %s", err, strings.TrimSpace(buf.String()))
	}

	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		if _, err := os.Stat(m.videoDevice); err == nil {
			_ = os.Chmod(m.videoDevice, 0o666)
			return true, nil
		}
		select {
		case <-waitCtx.Done():
			return false, errors.New("v4l2loopback device did not appear after modprobe")
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

func (m *Manager) setDefaultPulseDevices(ctx context.Context) {
	log := logger.FromContext(ctx)
	pulseEnv := append(os.Environ(), fmt.Sprintf("PULSE_SERVER=%s", os.Getenv("PULSE_SERVER")))

	setDefault := func(kind, name string) error {
		if name == "" {
			return nil
		}
		cmd := m.execCommand("pactl", "set-default-"+kind, name)
		cmd.Env = pulseEnv
		return cmd.Run()
	}

	if err := setDefault("source", m.microphoneSource); err != nil {
		log.Warn("failed to set default pulseaudio source", "err", err, "source", m.microphoneSource)
	}
}

func (m *Manager) buildFFmpegArgs(cfg Config, paused bool) ([]string, error) {
	var (
		args     []string
		videoIdx = -1
		audioIdx = -1
	)
	args = append(args, "-hide_banner", "-loglevel", "warning", "-nostdin", "-fflags", "+genpts", "-threads", "2", "-y")

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
		)
		if m.mode == modeVirtualFile && m.videoFile != "" {
			args = append(args,
				"-f", "yuv4mpegpipe",
				m.videoFile,
			)
		} else {
			args = append(args,
				"-f", "v4l2",
				m.videoDevice,
			)
		}
	}

	if cfg.Audio != nil {
		// Only route audio into Pulse when using a v4l2loopback device; in virtual-file
		// mode Chromium consumes the WAV via --use-file-for-fake-audio-capture, so
		// sending audio to a sink risks leaking it to the output path.
		routeToPulse := m.mode != modeVirtualFile
		if !routeToPulse && (cfg.Audio.Type == SourceTypeSocket || cfg.Audio.Type == SourceTypeWebRTC) {
			// Realtime ingest feeds should still be mirrored into the microphone sink
			// so consumers can read from Pulse in addition to the virtual capture file.
			routeToPulse = true
		}
		if routeToPulse {
			args = append(args,
				"-map", fmt.Sprintf("%d:a:0", audioIdx),
				"-ac", "2",
				"-ar", "48000",
				"-f", "pulse",
				m.audioSink,
			)
		}
		if m.audioFile != "" {
			args = append(args,
				"-map", fmt.Sprintf("%d:a:0", audioIdx),
				"-ac", "2",
				"-ar", "48000",
				"-f", "wav",
				m.audioFile,
			)
		}
	}

	return args, nil
}

func buildInputArgs(src *MediaSource) []string {
	var parts []string
	if src == nil {
		return parts
	}
	if src.Type == SourceTypeStream {
		parts = append(parts, "-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "2")
	}
	parts = append(parts, "-thread_queue_size", "64")
	if src.Type == SourceTypeFile {
		parts = append(parts, "-re")
	}
	if src.Type == SourceTypeSocket || src.Type == SourceTypeWebRTC {
		if src.Format != "" {
			parts = append(parts, "-f", src.Format)
		}
	}
	parts = append(parts, "-i", src.URL)
	return parts
}

func sourcesShared(cfg Config) bool {
	if cfg.Video == nil || cfg.Audio == nil {
		return false
	}
	return cfg.Video.URL == cfg.Audio.URL && cfg.Video.Type == cfg.Audio.Type
}

func usesRealtimeSource(cfg Config) bool {
	return (cfg.Video != nil && (cfg.Video.Type == SourceTypeSocket || cfg.Video.Type == SourceTypeWebRTC)) ||
		(cfg.Audio != nil && (cfg.Audio.Type == SourceTypeSocket || cfg.Audio.Type == SourceTypeWebRTC))
}

func needsVideoPipe(cfg Config) bool {
	return cfg.Video != nil && (cfg.Video.Type == SourceTypeSocket || cfg.Video.Type == SourceTypeWebRTC)
}

func needsAudioPipe(cfg Config) bool {
	return cfg.Audio != nil && (cfg.Audio.Type == SourceTypeSocket || cfg.Audio.Type == SourceTypeWebRTC)
}

func buildIngestStatus(cfg Config) *IngestStatus {
	var status IngestStatus
	if cfg.Video != nil && (cfg.Video.Type == SourceTypeSocket || cfg.Video.Type == SourceTypeWebRTC) {
		status.Video = &IngestEndpoint{
			Protocol: string(cfg.Video.Type),
			Format:   cfg.Video.Format,
			Path:     cfg.Video.URL,
		}
	}
	if cfg.Audio != nil && (cfg.Audio.Type == SourceTypeSocket || cfg.Audio.Type == SourceTypeWebRTC) {
		status.Audio = &IngestEndpoint{
			Protocol: string(cfg.Audio.Type),
			Format:   cfg.Audio.Format,
			Path:     cfg.Audio.URL,
		}
	}
	if status.Audio == nil && status.Video == nil {
		return nil
	}
	return &status
}

func normalizeSourceType(t SourceType) SourceType {
	switch strings.TrimSpace(strings.ToLower(string(t))) {
	case string(SourceTypeStream):
		return SourceTypeStream
	case string(SourceTypeFile):
		return SourceTypeFile
	case string(SourceTypeSocket):
		return SourceTypeSocket
	case string(SourceTypeWebRTC):
		return SourceTypeWebRTC
	default:
		return t
	}
}

func normalizeSource(src *MediaSource, isVideo bool) *MediaSource {
	if src == nil {
		return nil
	}
	out := *src
	if out.Type == SourceTypeSocket {
		if out.URL == "" {
			if isVideo {
				out.URL = defaultVideoPipe
			} else {
				out.URL = defaultAudioPipe
			}
		}
		if out.Format == "" {
			if isVideo {
				out.Format = "mpegts"
			} else {
				out.Format = "mp3"
			}
		}
	}
	if out.Type == SourceTypeWebRTC {
		if out.URL == "" {
			if isVideo {
				out.URL = defaultVideoPipe
			} else {
				out.URL = defaultAudioPipe
			}
		}
		if out.Format == "" {
			if isVideo {
				out.Format = "ivf"
			} else {
				out.Format = "ogg"
			}
		}
	}
	return &out
}

func validateRealtimeFormat(src *MediaSource, isVideo bool) error {
	if src == nil {
		return nil
	}
	switch src.Type {
	case SourceTypeSocket:
		if isVideo && src.Format != "" && src.Format != "mpegts" {
			return fmt.Errorf("%w: expected mpegts for socket video, got %s", ErrUnsupportedVideo, src.Format)
		}
		if !isVideo && src.Format != "" && src.Format != "mp3" {
			return fmt.Errorf("%w: expected mp3 for socket audio, got %s", ErrUnsupportedAudio, src.Format)
		}
	case SourceTypeWebRTC:
		if isVideo && src.Format != "" && src.Format != "ivf" {
			return fmt.Errorf("%w: expected ivf for webrtc video, got %s", ErrUnsupportedVideo, src.Format)
		}
		if !isVideo && src.Format != "" && src.Format != "ogg" {
			return fmt.Errorf("%w: expected ogg for webrtc audio, got %s", ErrUnsupportedAudio, src.Format)
		}
	}
	return nil
}

func preparePipe(path string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_ = os.Remove(path)
	if err := syscall.Mkfifo(path, 0o666); err != nil {
		return fmt.Errorf("create fifo %s: %w", path, err)
	}
	return nil
}

func (m *Manager) closePipeKeepalivesLocked() {
	if m.videoKeepalive != nil {
		_ = m.videoKeepalive.Close()
		m.videoKeepalive = nil
	}
	if m.audioKeepalive != nil {
		_ = m.audioKeepalive.Close()
		m.audioKeepalive = nil
	}
}

func (m *Manager) openPipeKeepalivesLocked(ctx context.Context, cfg Config, paused bool) {
	log := logger.FromContext(ctx)
	m.closePipeKeepalivesLocked()

	if paused {
		return
	}

	if needsVideoPipe(cfg) && m.videoPipe != "" {
		writer, err := OpenPipeReadWriter(m.videoPipe, DefaultPipeOpenTimeout)
		if err != nil {
			log.Warn("failed to open keepalive for virtual video pipe", "err", err, "path", m.videoPipe)
		} else {
			m.videoKeepalive = writer
		}
	}
	if needsAudioPipe(cfg) && m.audioPipe != "" {
		writer, err := OpenPipeReadWriter(m.audioPipe, DefaultPipeOpenTimeout)
		if err != nil {
			log.Warn("failed to open keepalive for virtual audio pipe", "err", err, "path", m.audioPipe)
		} else {
			m.audioKeepalive = writer
		}
	}
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
	env := os.Environ()
	if m.audioSink != "" {
		env = append(env, fmt.Sprintf("PULSE_SINK=%s", m.audioSink))
	}
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		m.enableScaleToZero(ctx)
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	m.processGroupID = cmd.Process.Pid
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
		m.processGroupID = 0
		m.closePipeKeepalivesLocked()
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
