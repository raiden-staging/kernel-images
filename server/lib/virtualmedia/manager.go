package virtualmedia

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// Config wraps optional audio/video sources.
type Config struct {
	Video *Source
	Audio *Source
}

// Paths expose the file paths backing Chromium's fake devices.
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
	mu          sync.Mutex
	baseDir     string
	ffmpegPath  string
	commandFn   commandFactory
	video       *process
	audio       *process
	readerPipes []*os.File
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

// NewManager returns a Manager writing media artifacts under baseDir.
func NewManager(baseDir, ffmpegPath string) *Manager {
	path := ffmpegPath
	if path == "" {
		path = "ffmpeg"
	}
	return &Manager{
		baseDir:    baseDir,
		ffmpegPath: path,
		commandFn:  exec.CommandContext,
	}
}

// Configure stops any existing pipelines and starts new ones for the provided config.
func (m *Manager) Configure(ctx context.Context, cfg Config) (Paths, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureBaseDir(); err != nil {
		return Paths{}, err
	}

	if err := m.stopAllLocked(ctx); err != nil {
		return Paths{}, err
	}

	var paths Paths
	if cfg.Video != nil {
		videoPath := filepath.Join(m.baseDir, "virtual-camera.y4m")
		proc, err := m.startLocked(ctx, *cfg.Video, videoPath, true)
		if err != nil {
			_ = m.stopAllLocked(ctx)
			return Paths{}, err
		}
		m.video = proc
		paths.VideoPath = videoPath
	}
	if cfg.Audio != nil {
		audioPath := filepath.Join(m.baseDir, "virtual-microphone.wav")
		proc, err := m.startLocked(ctx, *cfg.Audio, audioPath, false)
		if err != nil {
			_ = m.stopAllLocked(ctx)
			return Paths{}, err
		}
		m.audio = proc
		paths.AudioPath = audioPath
	}

	return paths, nil
}

// Stop terminates all running pipelines and cleans up FIFOs/files.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
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

func (m *Manager) ensureBaseDir() error {
	if m.baseDir == "" {
		return errors.New("baseDir is required")
	}
	return os.MkdirAll(m.baseDir, 0o755)
}

func (m *Manager) startLocked(ctx context.Context, source Source, outputPath string, isVideo bool) (*process, error) {
	if err := validateSource(source); err != nil {
		return nil, err
	}
	if err := prepareFIFO(outputPath); err != nil {
		return nil, fmt.Errorf("prepare fifo: %w", err)
	}

	reader, err := os.OpenFile(outputPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open fifo reader: %w", err)
	}

	args := m.buildArgs(source, outputPath, isVideo)
	cmd := m.commandFn(context.WithoutCancel(ctx), m.ffmpegPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		_ = reader.Close()
		_ = os.Remove(outputPath)
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	m.readerPipes = append(m.readerPipes, reader)

	proc := &process{
		cmd:        cmd,
		source:     source,
		outputPath: outputPath,
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
	for _, r := range m.readerPipes {
		_ = r.Close()
	}
	m.readerPipes = nil
	return nil
}

func (m *Manager) buildArgs(source Source, outputPath string, isVideo bool) []string {
	args := []string{"-nostdin", "-loglevel", "error"}
	if source.Kind == SourceKindFile && source.Loop {
		args = append(args, "-stream_loop", "-1")
	}
	args = append(args, "-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "2", "-i", source.URL)

	if isVideo {
		args = append(args,
			"-an",
			"-vf", "scale=1280:-2",
			"-pix_fmt", "yuv420p",
			"-f", "yuv4mpegpipe",
			outputPath,
		)
		return args
	}

	args = append(args,
		"-vn",
		"-acodec", "pcm_s16le",
		"-ar", "48000",
		"-ac", "2",
		"-f", "wav",
		outputPath,
	)
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

	_ = os.Remove(p.outputPath)
	return nil
}

func prepareFIFO(path string) error {
	_ = os.Remove(path)
	if err := syscall.Mkfifo(path, 0o666); err != nil {
		return err
	}
	return os.Chmod(path, 0o666)
}
