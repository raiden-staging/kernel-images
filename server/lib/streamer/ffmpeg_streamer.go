package streamer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onkernel/kernel-images/server/lib/logger"
	"github.com/onkernel/kernel-images/server/lib/scaletozero"
)

const (
	exitCodeInitValue           = math.MinInt
	exitCodeProcessDoneMinValue = -1
)

type StreamMode string

const (
	StreamModeInternal StreamMode = "internal"
	StreamModeRemote   StreamMode = "remote"
)

type StreamProtocol string

const (
	StreamProtocolRTMP  StreamProtocol = "rtmp"
	StreamProtocolRTMPS StreamProtocol = "rtmps"
)

// Streamer defines the interface for managing livestreams.
type Streamer interface {
	ID() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	ForceStop(ctx context.Context) error
	IsStreaming(ctx context.Context) bool
	Metadata() *StreamMetadata
}

type StreamMetadata struct {
	URL         string
	RelativeURL string
	Mode        StreamMode
	Protocol    StreamProtocol
	StartTime   time.Time
	EndTime     time.Time
}

// StreamManager defines the interface for managing multiple streamers.
type StreamManager interface {
	GetStream(id string) (Streamer, bool)
	ListActiveStreams(ctx context.Context) []Streamer
	DeregisterStream(ctx context.Context, streamer Streamer) error
	RegisterStream(ctx context.Context, streamer Streamer) error
	StopAll(ctx context.Context) error
}

type FFmpegStreamParams struct {
	FrameRate  *int
	DisplayNum *int
	Protocol   StreamProtocol
	Mode       StreamMode
	ListenHost string
	ListenPort int
	App        string
	TargetURL  string
	TLSCert    string
	TLSKey     string
}

func (p FFmpegStreamParams) Validate() error {
	if p.FrameRate == nil {
		return fmt.Errorf("frame rate is required")
	}
	if p.DisplayNum == nil {
		return fmt.Errorf("display number is required")
	}
	if p.Protocol != StreamProtocolRTMP && p.Protocol != StreamProtocolRTMPS {
		return fmt.Errorf("unsupported protocol %q", p.Protocol)
	}
	switch p.Mode {
	case StreamModeInternal:
		if p.ListenHost == "" {
			return fmt.Errorf("listen host is required for internal streams")
		}
		if p.ListenPort <= 0 || p.ListenPort > 65535 {
			return fmt.Errorf("listen port must be between 1 and 65535")
		}
		if strings.Trim(p.App, "/") == "" {
			return fmt.Errorf("app is required for internal streams")
		}
	case StreamModeRemote:
		if p.TargetURL == "" {
			return fmt.Errorf("target URL is required for remote streams")
		}
	default:
		return fmt.Errorf("unsupported mode %q", p.Mode)
	}
	return nil
}

type FFmpegStreamOverrides struct {
	FrameRate  *int
	DisplayNum *int
	Protocol   *StreamProtocol
	Mode       *StreamMode
	ListenHost *string
	ListenPort *int
	App        *string
	TargetURL  *string
	TLSCert    *string
	TLSKey     *string
}

type FFmpegStreamerFactory func(id string, overrides FFmpegStreamOverrides) (Streamer, error)

func NewFFmpegStreamerFactory(pathToFFmpeg string, config FFmpegStreamParams, ctrl scaletozero.Controller) FFmpegStreamerFactory {
	return func(id string, overrides FFmpegStreamOverrides) (Streamer, error) {
		merged := mergeFFmpegStreamParams(config, overrides)
		if err := merged.Validate(); err != nil {
			return nil, err
		}

		targetURL, relativeURL := buildTargetURLs(id, merged)

		return &FFmpegStreamer{
			id:          id,
			binaryPath:  pathToFFmpeg,
			params:      merged,
			targetURL:   targetURL,
			relativeURL: relativeURL,
			stz:         scaletozero.NewOncer(ctrl),
		}, nil
	}
}

func mergeFFmpegStreamParams(config FFmpegStreamParams, overrides FFmpegStreamOverrides) FFmpegStreamParams {
	out := config
	if overrides.FrameRate != nil {
		out.FrameRate = overrides.FrameRate
	}
	if overrides.DisplayNum != nil {
		out.DisplayNum = overrides.DisplayNum
	}
	if overrides.Protocol != nil {
		out.Protocol = *overrides.Protocol
	}
	if overrides.Mode != nil {
		out.Mode = *overrides.Mode
	}
	if overrides.ListenHost != nil {
		out.ListenHost = *overrides.ListenHost
	}
	if overrides.ListenPort != nil {
		out.ListenPort = *overrides.ListenPort
	}
	if overrides.App != nil {
		out.App = *overrides.App
	}
	if overrides.TargetURL != nil {
		out.TargetURL = *overrides.TargetURL
	}
	if overrides.TLSCert != nil {
		out.TLSCert = *overrides.TLSCert
	}
	if overrides.TLSKey != nil {
		out.TLSKey = *overrides.TLSKey
	}
	return out
}

func buildTargetURLs(id string, params FFmpegStreamParams) (string, string) {
	switch params.Mode {
	case StreamModeRemote:
		return params.TargetURL, ""
	default:
		app := strings.Trim(params.App, "/")
		path := strings.Trim(fmt.Sprintf("%s/%s", app, id), "/")
		hostport := net.JoinHostPort(params.ListenHost, strconv.Itoa(params.ListenPort))
		return fmt.Sprintf("%s://%s/%s", params.Protocol, hostport, path), "/" + path
	}
}

// FFmpegStreamer encapsulates an FFmpeg livestream session.
type FFmpegStreamer struct {
	mu sync.Mutex

	id            string
	binaryPath    string
	cmd           *exec.Cmd
	params        FFmpegStreamParams
	targetURL     string
	relativeURL   string
	startTime     time.Time
	endTime       time.Time
	ffmpegErr     error
	exitCode      int
	exited        chan struct{}
	stz           *scaletozero.Oncer
	stopRequested bool
}

func (fs *FFmpegStreamer) ID() string { return fs.id }

func (fs *FFmpegStreamer) Start(ctx context.Context) error {
	log := logger.FromContext(ctx)

	fs.mu.Lock()
	if fs.cmd != nil {
		fs.mu.Unlock()
		return fmt.Errorf("stream already in progress")
	}

	if err := fs.stz.Disable(ctx); err != nil {
		fs.mu.Unlock()
		return fmt.Errorf("failed to disable scale-to-zero: %w", err)
	}

	fs.ffmpegErr = nil
	fs.exitCode = exitCodeInitValue
	fs.exited = make(chan struct{})
	fs.stopRequested = false
	fs.startTime = time.Now()
	fs.mu.Unlock()

	go fs.runLoop(ctx, log)

	if err := waitForChan(ctx, 250*time.Millisecond, fs.exited); err == nil {
		fs.mu.Lock()
		defer fs.mu.Unlock()
		if fs.ffmpegErr != nil {
			return fmt.Errorf("failed to start ffmpeg process: %w", fs.ffmpegErr)
		}
		return fmt.Errorf("failed to start ffmpeg process")
	}

	return nil
}

func (fs *FFmpegStreamer) Stop(ctx context.Context) error {
	fs.mu.Lock()
	fs.stopRequested = true
	fs.mu.Unlock()

	defer fs.stz.Enable(context.WithoutCancel(ctx))
	err := fs.shutdownInPhases(ctx, []shutdownPhase{
		{"wake_and_interrupt", []syscall.Signal{syscall.SIGINT}, time.Minute, "graceful stop"},
		{"terminate", []syscall.Signal{syscall.SIGTERM}, 2 * time.Second, "forceful termination"},
		{"kill", []syscall.Signal{syscall.SIGKILL}, 100 * time.Millisecond, "immediate kill"},
	})
	return err
}

func (fs *FFmpegStreamer) ForceStop(ctx context.Context) error {
	fs.mu.Lock()
	fs.stopRequested = true
	fs.mu.Unlock()

	defer fs.stz.Enable(context.WithoutCancel(ctx))
	return fs.shutdownInPhases(ctx, []shutdownPhase{
		{"kill", []syscall.Signal{syscall.SIGKILL}, 100 * time.Millisecond, "immediate kill"},
	})
}

func (fs *FFmpegStreamer) IsStreaming(ctx context.Context) bool {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.cmd != nil && fs.exitCode < exitCodeProcessDoneMinValue
}

func (fs *FFmpegStreamer) Metadata() *StreamMetadata {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return &StreamMetadata{
		URL:         fs.targetURL,
		RelativeURL: fs.relativeURL,
		Mode:        fs.params.Mode,
		Protocol:    fs.params.Protocol,
		StartTime:   fs.startTime,
		EndTime:     fs.endTime,
	}
}

func ffmpegStreamArgs(params FFmpegStreamParams, targetURL string) ([]string, error) {
	var args []string

	// TLS files (when acting as RTMPS server)
	if params.Mode == StreamModeInternal && params.Protocol == StreamProtocolRTMPS {
		if params.TLSCert != "" && params.TLSKey != "" {
			args = append(args, "-tls_cert_file", params.TLSCert, "-tls_key_file", params.TLSKey)
		}
	}

	switch runtime.GOOS {
	case "darwin":
		args = append(args,
			"-f", "avfoundation",
			"-framerate", strconv.Itoa(*params.FrameRate),
			"-pixel_format", "nv12",
			"-i", fmt.Sprintf("%d:none", *params.DisplayNum),
		)
	case "linux":
		args = append(args,
			"-f", "x11grab",
			"-framerate", strconv.Itoa(*params.FrameRate),
			"-i", fmt.Sprintf(":%d", *params.DisplayNum),
		)
	default:
		return nil, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	args = append(args,
		"-vcodec", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-pix_fmt", "yuv420p",
		"-f", "flv",
	)

	if params.FrameRate != nil && *params.FrameRate > 0 {
		args = append(args, "-g", strconv.Itoa(*params.FrameRate*2))
	}

	if params.Mode == StreamModeInternal {
		args = append(args, "-listen", "1")
	}

	args = append(args, targetURL)
	return args, nil
}

func (fs *FFmpegStreamer) runLoop(ctx context.Context, log *slog.Logger) {
	defer fs.stz.Enable(context.WithoutCancel(ctx))

	for {
		if err := fs.launchOnce(ctx, log); err != nil {
			fs.mu.Lock()
			fs.ffmpegErr = err
			fs.exitCode = exitCodeProcessDoneMinValue
			fs.endTime = time.Now()
			select {
			case <-fs.exited:
			default:
				close(fs.exited)
			}
			fs.mu.Unlock()
			return
		}

		fs.mu.Lock()
		exitCode := fs.exitCode
		stopRequested := fs.stopRequested
		mode := fs.params.Mode
		if fs.endTime.IsZero() {
			fs.endTime = time.Now()
		}
		fs.mu.Unlock()

		if stopRequested || mode != StreamModeInternal {
			fs.mu.Lock()
			select {
			case <-fs.exited:
			default:
				close(fs.exited)
			}
			fs.mu.Unlock()
			return
		}

		log.Warn("ffmpeg stream process exited unexpectedly; restarting", "exitCode", exitCode)
		time.Sleep(500 * time.Millisecond)
	}
}

func (fs *FFmpegStreamer) launchOnce(ctx context.Context, log *slog.Logger) error {
	fs.mu.Lock()
	args, err := ffmpegStreamArgs(fs.params, fs.targetURL)
	if err != nil {
		_ = fs.stz.Enable(context.WithoutCancel(ctx))
		fs.cmd = nil
		select {
		case <-fs.exited:
		default:
			close(fs.exited)
		}
		fs.mu.Unlock()
		return err
	}

	log.Info(fmt.Sprintf("%s %s", fs.binaryPath, strings.Join(args, " ")))
	cmd := exec.Command(fs.binaryPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fs.exitCode = exitCodeInitValue
	fs.cmd = cmd
	fs.mu.Unlock()

	if err := cmd.Start(); err != nil {
		_ = fs.stz.Enable(context.WithoutCancel(ctx))
		fs.mu.Lock()
		fs.ffmpegErr = err
		fs.cmd = nil
		select {
		case <-fs.exited:
		default:
			close(fs.exited)
		}
		fs.mu.Unlock()
		return fmt.Errorf("failed to start ffmpeg process: %w", err)
	}

	err = cmd.Wait()

	fs.mu.Lock()
	fs.ffmpegErr = err
	if cmd.ProcessState != nil {
		fs.exitCode = cmd.ProcessState.ExitCode()
	} else {
		fs.exitCode = exitCodeProcessDoneMinValue
	}
	fs.cmd = nil
	fs.mu.Unlock()

	if err != nil {
		log.Info("ffmpeg stream process completed with error", "err", err, "exitCode", fs.exitCode)
	} else {
		log.Info("ffmpeg stream process completed successfully", "exitCode", fs.exitCode)
	}

	return nil
}

type shutdownPhase struct {
	name    string
	signals []syscall.Signal
	timeout time.Duration
	desc    string
}

func (fs *FFmpegStreamer) shutdownInPhases(ctx context.Context, phases []shutdownPhase) error {
	log := logger.FromContext(ctx)

	fs.mu.Lock()
	exitCode := fs.exitCode
	cmd := fs.cmd
	done := fs.exited
	fs.mu.Unlock()

	if exitCode >= exitCodeProcessDoneMinValue {
		log.Info("ffmpeg stream process has already exited")
		return nil
	}
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("no stream to stop")
	}

	pgid := -cmd.Process.Pid
	for _, phase := range phases {
		phaseStartTime := time.Now()

		select {
		case <-done:
			return nil
		default:
		}

		log.Info("ffmpeg stream shutdown phase", "phase", phase.name, "desc", phase.desc)

		for idx, sig := range phase.signals {
			_ = syscall.Kill(pgid, sig)
			if idx < len(phase.signals)-1 {
				time.Sleep(100 * time.Millisecond)
			}
		}

		if err := waitForChan(ctx, phase.timeout-time.Since(phaseStartTime), done); err == nil {
			log.Info("ffmpeg stream shutdown successful", "phase", phase.name)
			fs.mu.Lock()
			defer fs.mu.Unlock()
			return fs.ffmpegErr
		}
	}

	return fmt.Errorf("failed to shutdown ffmpeg stream")
}

func waitForChan(ctx context.Context, timeout time.Duration, c <-chan struct{}) error {
	select {
	case <-c:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("process did not exit within %v timeout", timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

type FFmpegStreamManager struct {
	mu      sync.Mutex
	streams map[string]Streamer
}

func NewFFmpegStreamManager() *FFmpegStreamManager {
	return &FFmpegStreamManager{streams: make(map[string]Streamer)}
}

func (fm *FFmpegStreamManager) GetStream(id string) (Streamer, bool) {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	stream, ok := fm.streams[id]
	return stream, ok
}

func (fm *FFmpegStreamManager) ListActiveStreams(ctx context.Context) []Streamer {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	streams := make([]Streamer, 0, len(fm.streams))
	for _, s := range fm.streams {
		if s.IsStreaming(ctx) {
			streams = append(streams, s)
		}
	}
	return streams
}

func (fm *FFmpegStreamManager) DeregisterStream(ctx context.Context, streamer Streamer) error {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	delete(fm.streams, streamer.ID())
	return nil
}

func (fm *FFmpegStreamManager) RegisterStream(ctx context.Context, streamer Streamer) error {
	log := logger.FromContext(ctx)

	fm.mu.Lock()
	defer fm.mu.Unlock()

	if _, exists := fm.streams[streamer.ID()]; exists {
		return fmt.Errorf("stream with id '%s' already exists", streamer.ID())
	}

	fm.streams[streamer.ID()] = streamer
	log.Info("registered new stream", "id", streamer.ID())
	return nil
}

func (fm *FFmpegStreamManager) StopAll(ctx context.Context) error {
	log := logger.FromContext(ctx)

	fm.mu.Lock()
	streams := make([]Streamer, 0, len(fm.streams))
	for _, stream := range fm.streams {
		streams = append(streams, stream)
	}
	fm.mu.Unlock()

	var errs []error
	for _, s := range streams {
		if s.IsStreaming(ctx) {
			if err := s.Stop(ctx); err != nil {
				errs = append(errs, fmt.Errorf("failed to stop stream '%s': %w", s.ID(), err))
				log.Error("failed to stop stream during shutdown", "id", s.ID(), "err", err)
			}
		}
		_ = fm.DeregisterStream(ctx, s)
	}

	log.Info("stopped all streams", "count", len(streams))

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}
