package virtualmedia

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onkernel/kernel-images/server/lib/logger"
)

type SourceType string

const (
	SourceTypeStream SourceType = "stream"
	SourceTypeFile   SourceType = "file"
)

var (
	ErrNoSources   = errors.New("no virtual media sources provided")
	ErrInvalidSrc  = errors.New("invalid virtual media source")
	ErrNotRunning  = errors.New("no virtual media pipelines are running")
	ErrNotPaused   = errors.New("virtual media pipelines are not paused")
	defaultDevPath = "/dev/video20"
)

type Source struct {
	URL  string
	Type SourceType
	Loop bool
}

type Config struct {
	Video *Source
	Audio *Source
}

type PipelineState string

const (
	StateStopped PipelineState = "stopped"
	StateRunning PipelineState = "running"
	StatePaused  PipelineState = "paused"
)

type PipelineStatus struct {
	State  PipelineState
	Source *Source
	PID    int
}

type Status struct {
	State       PipelineState
	Video       PipelineStatus
	Audio       PipelineStatus
	VideoDevice string
	AudioSink   string
}

type Options struct {
	VideoDevice string
	VideoModule string
	AudioSink   string
	PulseServer string
	User        string
}

type Controller struct {
	mu        sync.Mutex
	video     *pipelineHandle
	audio     *pipelineHandle
	opts      Options
	cred      *syscall.Credential
	uid       int
	gid       int
	closeOnce sync.Once
}

type pipelineHandle struct {
	cmd    *exec.Cmd
	source Source
	state  PipelineState
	done   chan struct{}
}

func NewController(opts Options) (*Controller, error) {
	if opts.VideoDevice == "" {
		opts.VideoDevice = defaultDevPath
	}
	if opts.VideoModule == "" {
		opts.VideoModule = "v4l2loopback"
	}
	if opts.AudioSink == "" {
		opts.AudioSink = "audio_input"
	}
	if opts.User == "" {
		opts.User = "kernel"
	}

	ctrl := &Controller{opts: opts}

	if opts.User != "" {
		u, err := user.Lookup(opts.User)
		if err != nil {
			return nil, fmt.Errorf("lookup user %q: %w", opts.User, err)
		}
		uid, err := strconv.ParseUint(u.Uid, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("parse uid for %q: %w", opts.User, err)
		}
		gid, err := strconv.ParseUint(u.Gid, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("parse gid for %q: %w", opts.User, err)
		}
		ctrl.cred = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
		ctrl.uid = int(uid)
		ctrl.gid = int(gid)
	}

	return ctrl, nil
}

func (c *Controller) SetSources(ctx context.Context, cfg Config) (Status, error) {
	videoSrc, err := normalizeSource(cfg.Video)
	if err != nil {
		return c.Status(ctx), err
	}
	audioSrc, err := normalizeSource(cfg.Audio)
	if err != nil {
		return c.Status(ctx), err
	}
	if videoSrc == nil && audioSrc == nil {
		return c.Status(ctx), ErrNoSources
	}

	ctx = context.WithoutCancel(ctx)
	log := logger.FromContext(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()

	if videoSrc == nil && c.video != nil {
		c.stopPipelineLocked(ctx, c.video)
		c.video = nil
	}
	if audioSrc == nil && c.audio != nil {
		c.stopPipelineLocked(ctx, c.audio)
		c.audio = nil
	}

	started := []*pipelineHandle{}

	if videoSrc != nil {
		if err := c.ensureVideoDeviceLocked(ctx); err != nil {
			c.rollbackLocked(ctx, started)
			return c.statusLocked(), err
		}
		if c.video != nil {
			c.stopPipelineLocked(ctx, c.video)
		}
		handle, err := c.startVideoLocked(ctx, *videoSrc)
		if err != nil {
			c.rollbackLocked(ctx, started)
			return c.statusLocked(), err
		}
		started = append(started, handle)
		c.video = handle
		log.Info("virtual media video pipeline started", "device", c.opts.VideoDevice, "url", videoSrc.URL)
	}

	if audioSrc != nil {
		if err := c.ensureAudioSinkLocked(ctx); err != nil {
			c.rollbackLocked(ctx, started)
			return c.statusLocked(), err
		}
		if c.audio != nil {
			c.stopPipelineLocked(ctx, c.audio)
		}
		handle, err := c.startAudioLocked(ctx, *audioSrc)
		if err != nil {
			c.rollbackLocked(ctx, started)
			return c.statusLocked(), err
		}
		started = append(started, handle)
		c.audio = handle
		log.Info("virtual media audio pipeline started", "sink", c.opts.AudioSink, "url", audioSrc.URL)
	}

	return c.statusLocked(), nil
}

func (c *Controller) Pause(ctx context.Context) (Status, error) {
	ctx = context.WithoutCancel(ctx)
	log := logger.FromContext(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.anyRunningLocked() {
		return c.statusLocked(), ErrNotRunning
	}

	if err := c.pauseHandleLocked(c.video); err != nil {
		return c.statusLocked(), err
	}
	if err := c.pauseHandleLocked(c.audio); err != nil {
		return c.statusLocked(), err
	}

	log.Info("virtual media paused")
	return c.statusLocked(), nil
}

func (c *Controller) Resume(ctx context.Context) (Status, error) {
	ctx = context.WithoutCancel(ctx)
	log := logger.FromContext(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.anyPausedLocked() {
		return c.statusLocked(), ErrNotPaused
	}

	if err := c.resumeHandleLocked(c.video); err != nil {
		return c.statusLocked(), err
	}
	if err := c.resumeHandleLocked(c.audio); err != nil {
		return c.statusLocked(), err
	}

	log.Info("virtual media resumed")
	return c.statusLocked(), nil
}

func (c *Controller) Stop(ctx context.Context) (Status, error) {
	ctx = context.WithoutCancel(ctx)
	log := logger.FromContext(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.stopPipelineLocked(ctx, c.video)
	c.stopPipelineLocked(ctx, c.audio)
	c.video = nil
	c.audio = nil

	log.Info("virtual media stopped")
	return c.statusLocked(), nil
}

func (c *Controller) Shutdown(ctx context.Context) error {
	_, err := c.Stop(ctx)
	return err
}

func (c *Controller) Status(ctx context.Context) Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

func (c *Controller) statusLocked() Status {
	videoStatus := pipelineStatusLocked(c.video)
	audioStatus := pipelineStatusLocked(c.audio)
	return Status{
		State:       aggregateState(videoStatus.State, audioStatus.State),
		Video:       videoStatus,
		Audio:       audioStatus,
		VideoDevice: c.opts.VideoDevice,
		AudioSink:   c.opts.AudioSink,
	}
}

func pipelineStatusLocked(h *pipelineHandle) PipelineStatus {
	if h == nil {
		return PipelineStatus{State: StateStopped}
	}
	status := PipelineStatus{
		State: h.state,
	}
	if h.cmd != nil && h.cmd.Process != nil {
		status.PID = h.cmd.Process.Pid
	}
	status.Source = &h.source
	return status
}

func (c *Controller) rollbackLocked(ctx context.Context, handles []*pipelineHandle) {
	for _, h := range handles {
		c.stopPipelineLocked(ctx, h)
	}
}

func normalizeSource(src *Source) (*Source, error) {
	if src == nil {
		return nil, nil
	}
	if strings.TrimSpace(src.URL) == "" {
		return nil, fmt.Errorf("%w: url is required", ErrInvalidSrc)
	}
	sourceType := src.Type
	if sourceType == "" {
		sourceType = SourceTypeStream
	}
	if sourceType != SourceTypeStream && sourceType != SourceTypeFile {
		return nil, fmt.Errorf("%w: unsupported type %q", ErrInvalidSrc, src.Type)
	}
	return &Source{
		URL:  strings.TrimSpace(src.URL),
		Type: sourceType,
		Loop: src.Loop,
	}, nil
}

func (c *Controller) ensureVideoDeviceLocked(ctx context.Context) error {
	if _, err := os.Stat(c.opts.VideoDevice); err == nil {
		return nil
	}

	videoNum, err := parseVideoNumber(c.opts.VideoDevice)
	if err != nil {
		return err
	}

	args := []string{c.opts.VideoModule, fmt.Sprintf("video_nr=%d", videoNum), "exclusive_caps=1", "card_label=KernelVirtualCamera"}
	cmd := exec.CommandContext(ctx, "modprobe", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("load %s module: %w", c.opts.VideoModule, err)
	}

	if _, err := os.Stat(c.opts.VideoDevice); err != nil {
		return fmt.Errorf("virtual camera device not available after modprobe: %w", err)
	}
	if c.uid != 0 && c.gid != 0 {
		_ = os.Chown(c.opts.VideoDevice, c.uid, c.gid)
	}
	_ = os.Chmod(c.opts.VideoDevice, 0o666)
	return nil
}

func (c *Controller) ensureAudioSinkLocked(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "pactl", "list", "short", "sinks")
	cmd.Env = c.buildEnv()
	c.applyCredentials(cmd)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("list pulseaudio sinks: %w", err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == c.opts.AudioSink {
			return nil
		}
	}
	return fmt.Errorf("pulseaudio sink %q not found", c.opts.AudioSink)
}

func (c *Controller) startVideoLocked(ctx context.Context, source Source) (*pipelineHandle, error) {
	args := baseFFmpegArgs(source)
	args = append(args,
		"-an",
		"-vf", "scale=1280:720:force_original_aspect_ratio=decrease,pad=1280:720:(ow-iw)/2:(oh-ih)/2,fps=30,format=yuv420p",
		"-pix_fmt", "yuv420p",
		"-f", "v4l2",
		c.opts.VideoDevice,
	)
	return c.startPipelineLocked(ctx, args, source)
}

func (c *Controller) startAudioLocked(ctx context.Context, source Source) (*pipelineHandle, error) {
	args := baseFFmpegArgs(source)
	args = append(args,
		"-vn",
		"-c:a", "pcm_s16le",
		"-ac", "2",
		"-ar", "48000",
		"-f", "pulse",
		c.opts.AudioSink,
	)
	return c.startPipelineLocked(ctx, args, source)
}

func (c *Controller) startPipelineLocked(ctx context.Context, args []string, source Source) (*pipelineHandle, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Env = c.buildEnv()
	c.applyCredentials(cmd)
	cmd.SysProcAttr.Setpgid = true
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	handle := &pipelineHandle{
		cmd:    cmd,
		source: source,
		state:  StateRunning,
		done:   make(chan struct{}),
	}

	go func(h *pipelineHandle) {
		_ = h.cmd.Wait()
		c.mu.Lock()
		h.state = StateStopped
		close(h.done)
		c.mu.Unlock()
	}(handle)

	return handle, nil
}

func (c *Controller) stopPipelineLocked(ctx context.Context, h *pipelineHandle) {
	if h == nil || h.cmd == nil || h.state == StateStopped {
		return
	}

	pid := h.cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)

	select {
	case <-h.done:
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-h.done
	}
	h.state = StateStopped
}

func (c *Controller) pauseHandleLocked(h *pipelineHandle) error {
	if h == nil || h.state != StateRunning {
		return nil
	}
	if h.cmd == nil || h.cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-h.cmd.Process.Pid, syscall.SIGSTOP); err != nil {
		return fmt.Errorf("pause pipeline: %w", err)
	}
	h.state = StatePaused
	return nil
}

func (c *Controller) resumeHandleLocked(h *pipelineHandle) error {
	if h == nil || h.state != StatePaused {
		return nil
	}
	if h.cmd == nil || h.cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-h.cmd.Process.Pid, syscall.SIGCONT); err != nil {
		return fmt.Errorf("resume pipeline: %w", err)
	}
	h.state = StateRunning
	return nil
}

func (c *Controller) anyRunningLocked() bool {
	return (c.video != nil && c.video.state == StateRunning) || (c.audio != nil && c.audio.state == StateRunning)
}

func (c *Controller) anyPausedLocked() bool {
	return (c.video != nil && c.video.state == StatePaused) || (c.audio != nil && c.audio.state == StatePaused)
}

func aggregateState(video, audio PipelineState) PipelineState {
	if video == StateRunning || audio == StateRunning {
		return StateRunning
	}
	if video == StatePaused || audio == StatePaused {
		return StatePaused
	}
	return StateStopped
}

func parseVideoNumber(dev string) (int, error) {
	base := filepath.Base(dev)
	numStr := strings.TrimPrefix(base, "video")
	if numStr == "" {
		return 0, fmt.Errorf("unable to parse video device number from %q", dev)
	}
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("invalid video device number %q: %w", numStr, err)
	}
	return num, nil
}

func baseFFmpegArgs(source Source) []string {
	args := []string{
		"-nostdin",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_delay_max", "2",
	}
	if source.Type == SourceTypeFile && source.Loop {
		args = append(args, "-stream_loop", "-1")
	}
	args = append(args, "-i", source.URL)
	return args
}

func (c *Controller) buildEnv() []string {
	env := os.Environ()
	if c.opts.PulseServer != "" {
		env = append(env, "PULSE_SERVER="+c.opts.PulseServer)
	}
	return env
}

func (c *Controller) applyCredentials(cmd *exec.Cmd) {
	if c.cred == nil {
		if cmd.SysProcAttr == nil {
			cmd.SysProcAttr = &syscall.SysProcAttr{}
		}
		cmd.SysProcAttr.Setpgid = true
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: c.cred,
		Setpgid:    true,
	}
}
