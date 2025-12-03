package virtualmedia

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// SourceKind describes the type of upstream media input.
type SourceKind string

const (
	SourceKindStream SourceKind = "stream"
	SourceKindFile   SourceKind = "file"
)

// Source identifies a remote media input.
type Source struct {
	URL  string
	Kind SourceKind
	Loop bool
}

const (
	DefaultVideoDevicePath = "/dev/video42"
	DefaultAudioSinkName   = "audio_input"
)

// Options drive how the manager targets OS-level virtual devices.
type Options struct {
	FFmpegPath      string
	VideoDevicePath string
	AudioSinkName   string
}

// Config wraps optional audio/video sources.
type Config struct {
	Video *Source
	Audio *Source
}

// Paths expose the OS targets backing the virtual devices.
type Paths struct {
	VideoPath string
	AudioPath string
}

// TrackStatus reports the state of a running ffmpeg process.
type TrackStatus struct {
	Active     bool
	Paused     bool
	PID        int
	OutputPath string
	Source     *Source
	StartedAt  *time.Time
	LastError  string
}

// Status is the combined view of audio/video tracks.
type Status struct {
	Video *TrackStatus
	Audio *TrackStatus
}

type commandFactory func(ctx context.Context, name string, arg ...string) *exec.Cmd

// Manager owns the ffmpeg pipelines that feed Chromium's virtual camera/microphone.
type Manager struct {
	mu         sync.Mutex
	ffmpegPath string
	commandFn  commandFactory

	videoDevice string
	audioSink   string

	video *process
	audio *process
}

// process wraps an ffmpeg invocation and its lifecycle metadata.
type process struct {
	cmd        *exec.Cmd
	source     Source
	outputPath string
	paused     bool
	startedAt  time.Time
	done       chan struct{}
	lastErr    error
}

const stopTimeout = 5 * time.Second

// NewManager returns a Manager writing into OS-level virtual devices.
func NewManager(opts Options) *Manager {
	path := opts.FFmpegPath
	if path == "" {
		path = "ffmpeg"
	}

	videoDevice := opts.VideoDevicePath
	if videoDevice == "" {
		videoDevice = DefaultVideoDevicePath
	}

	audioSink := opts.AudioSinkName
	if audioSink == "" {
		audioSink = DefaultAudioSinkName
	}

	return &Manager{
		ffmpegPath:  path,
		commandFn:   exec.CommandContext,
		videoDevice: videoDevice,
		audioSink:   audioSink,
	}
}

// Configure stops any existing pipelines and starts new ones for the provided config.
func (m *Manager) Configure(ctx context.Context, cfg Config) (Paths, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ctx, cancel := ensureTimeout(ctx, stopTimeout)
	defer cancel()

	if err := m.stopAllLocked(ctx); err != nil {
		return Paths{}, err
	}

	var paths Paths
	if cfg.Video != nil {
		if err := m.ensureVideoTarget(ctx); err != nil {
			return Paths{}, err
		}
		proc, err := m.startVideoLocked(ctx, *cfg.Video)
		if err != nil {
			_ = m.stopAllLocked(ctx)
			return Paths{}, err
		}
		m.video = proc
		paths.VideoPath = m.videoDevice
	}
	if cfg.Audio != nil {
		if err := m.ensureAudioTarget(ctx); err != nil {
			return Paths{}, err
		}
		proc, err := m.startAudioLocked(ctx, *cfg.Audio)
		if err != nil {
			_ = m.stopAllLocked(ctx)
			return Paths{}, err
		}
		m.audio = proc
		paths.AudioPath = fmt.Sprintf("pulse:%s", m.audioSink)
	}

	return paths, nil
}

// Stop terminates all running pipelines.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx, cancel := ensureTimeout(ctx, stopTimeout)
	defer cancel()
	return m.stopAllLocked(ctx)
}

// Pause sends SIGSTOP to the requested tracks.
func (m *Manager) Pause(_ context.Context, pauseVideo, pauseAudio bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if pauseVideo {
		if err := pauseProcess(m.video); err != nil {
			return err
		}
	}
	if pauseAudio {
		if err := pauseProcess(m.audio); err != nil {
			return err
		}
	}
	if !pauseVideo && !pauseAudio {
		return errors.New("no virtual media tracks requested")
	}
	return nil
}

// Resume sends SIGCONT to the requested tracks.
func (m *Manager) Resume(_ context.Context, resumeVideo, resumeAudio bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if resumeVideo {
		if err := resumeProcess(m.video); err != nil {
			return err
		}
	}
	if resumeAudio {
		if err := resumeProcess(m.audio); err != nil {
			return err
		}
	}
	if !resumeVideo && !resumeAudio {
		return errors.New("no virtual media tracks requested")
	}
	return nil
}

// Status returns a snapshot of the current pipelines.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	return Status{
		Video: trackStatus(m.video),
		Audio: trackStatus(m.audio),
	}
}

func (m *Manager) ensureVideoTarget(ctx context.Context) error {
	if m.videoDevice == "" {
		return errors.New("video device is required")
	}
	info, err := os.Stat(m.videoDevice)
	if err != nil && errors.Is(err, os.ErrNotExist) && strings.HasPrefix(m.videoDevice, "/dev/video") {
		if loadErr := m.loadV4L2Loopback(ctx); loadErr != nil {
			return fmt.Errorf("video device not available at %s: %w", m.videoDevice, loadErr)
		}
		info, err = os.Stat(m.videoDevice)
	}
	if err != nil {
		return fmt.Errorf("video device not available at %s: %w", m.videoDevice, err)
	}
	if info.Mode()&os.ModeDevice == 0 {
		return fmt.Errorf("video target %s is not a device", m.videoDevice)
	}
	return nil
}

func (m *Manager) loadV4L2Loopback(ctx context.Context) error {
	modprobePath, err := exec.LookPath("modprobe")
	if err != nil {
		return fmt.Errorf("modprobe not available: %w", err)
	}

	args := []string{"v4l2loopback", "exclusive_caps=1"}
	if videoNr := strings.TrimPrefix(m.videoDevice, "/dev/video"); videoNr != "" {
		if _, err := strconv.Atoi(videoNr); err == nil {
			args = append(args, "video_nr="+videoNr)
		}
	}
	if label := strings.TrimSpace(os.Getenv("VIRTUAL_CAMERA_LABEL")); label != "" {
		args = append(args, "card_label="+label)
	}

	cmdCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	if out, err := exec.CommandContext(cmdCtx, modprobePath, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("modprobe v4l2loopback failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *Manager) ensureAudioTarget(ctx context.Context) error {
	if m.audioSink == "" {
		return errors.New("audio sink is required")
	}
	sinks, err := listPulseSinks(ctx)
	if err != nil {
		return err
	}
	for _, sink := range sinks {
		if sink == m.audioSink {
			return nil
		}
	}
	return fmt.Errorf("pulse sink not available: %s", m.audioSink)
}

func listPulseSinks(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(context.WithoutCancel(ctx), "pactl", "list", "short", "sinks")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list pulse sinks: %w", err)
	}

	var sinks []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			sinks = append(sinks, fields[1])
		}
	}
	return sinks, nil
}

func (m *Manager) startVideoLocked(ctx context.Context, source Source) (*process, error) {
	args := buildVideoArgs(source, m.videoDevice)
	return m.startProcess(ctx, source, fmt.Sprintf("v4l2:%s", m.videoDevice), args)
}

func (m *Manager) startAudioLocked(ctx context.Context, source Source) (*process, error) {
	args := buildAudioArgs(source, m.audioSink)
	return m.startProcess(ctx, source, fmt.Sprintf("pulse:%s", m.audioSink), args)
}

func (m *Manager) startProcess(ctx context.Context, source Source, target string, args []string) (*process, error) {
	if err := validateSource(source); err != nil {
		return nil, err
	}

	cmd := m.commandFn(context.WithoutCancel(ctx), m.ffmpegPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	proc := &process{
		cmd:        cmd,
		source:     source,
		outputPath: target,
		startedAt:  time.Now(),
		done:       make(chan struct{}),
	}
	go m.waitForProcess(proc)

	return proc, nil
}

func (m *Manager) waitForProcess(p *process) {
	err := p.cmd.Wait()
	p.lastErr = err
	close(p.done)
}

func (m *Manager) stopAllLocked(ctx context.Context) error {
	if err := stopProcess(ctx, m.video); err != nil {
		return err
	}
	if err := stopProcess(ctx, m.audio); err != nil {
		return err
	}
	m.video = nil
	m.audio = nil
	return nil
}

func buildVideoArgs(source Source, devicePath string) []string {
	args := buildBaseArgs(source)
	args = append(args,
		"-an",
		"-vf", "scale=1280:-2,fps=30",
		"-pix_fmt", "yuv420p",
		"-f", "v4l2",
		devicePath,
	)
	return args
}

func buildAudioArgs(source Source, sink string) []string {
	args := buildBaseArgs(source)
	args = append(args,
		"-vn",
		"-acodec", "pcm_s16le",
		"-ar", "48000",
		"-ac", "2",
		"-f", "pulse",
		"-sample_fmt", "s16le",
		"-device", sink,
		"virtual-media-input",
	)
	return args
}

func buildBaseArgs(source Source) []string {
	args := []string{"-nostdin", "-loglevel", "error", "-y"}
	if source.Kind == SourceKindFile && source.Loop {
		args = append(args, "-stream_loop", "-1")
	}
	args = append(args, "-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "2", "-i", source.URL)
	return args
}

func validateSource(source Source) error {
	if source.URL == "" {
		return errors.New("source url is required")
	}
	switch source.Kind {
	case SourceKindFile, SourceKindStream:
	default:
		return fmt.Errorf("unsupported source kind: %s", source.Kind)
	}
	return nil
}

func trackStatus(p *process) *TrackStatus {
	if p == nil {
		return nil
	}
	var pid int
	if p.cmd != nil && p.cmd.Process != nil {
		pid = p.cmd.Process.Pid
	}
	status := &TrackStatus{
		Active:     p.cmd != nil && p.cmd.ProcessState == nil,
		Paused:     p.paused,
		PID:        pid,
		OutputPath: p.outputPath,
		Source:     &p.source,
		StartedAt:  &p.startedAt,
	}
	if p.lastErr != nil {
		status.LastError = p.lastErr.Error()
	}
	return status
}

func pauseProcess(p *process) error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil || p.cmd.ProcessState != nil {
		return nil
	}
	if p.paused {
		return nil
	}
	if err := p.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		return fmt.Errorf("pause process: %w", err)
	}
	p.paused = true
	return nil
}

func resumeProcess(p *process) error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil || p.cmd.ProcessState != nil {
		return nil
	}
	if !p.paused {
		return nil
	}
	if err := p.cmd.Process.Signal(syscall.SIGCONT); err != nil {
		return fmt.Errorf("resume process: %w", err)
	}
	p.paused = false
	return nil
}

func stopProcess(ctx context.Context, p *process) error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil || p.cmd.ProcessState != nil {
		return nil
	}

	pgid, _ := syscall.Getpgid(p.cmd.Process.Pid)
	if pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
	} else {
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
	}

	wait := p.done
	if wait == nil {
		wait = make(chan struct{})
		close(wait)
	}

	select {
	case <-wait:
	case <-ctx.Done():
		if pgid > 0 {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			_ = p.cmd.Process.Kill()
		}
		<-wait
	}
	return nil
}

func ensureTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), d)
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), d)
}
