package stream

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"github.com/onkernel/kernel-images/server/lib/logger"
	"github.com/onkernel/kernel-images/server/lib/scaletozero"
)

// SocketStreamer captures the display/audio and broadcasts MPEG-TS chunks over WebSocket.
type SocketStreamer struct {
	mu sync.Mutex

	id         string
	params     Params
	ffmpegPath string

	cmd       *exec.Cmd
	exited    chan struct{}
	startedAt time.Time
	stz       *scaletozero.Oncer

	pr      *io.PipeReader
	pw      *io.PipeWriter
	clients map[WebSocketConn]struct{}
}

func NewSocketStreamer(id string, params Params, ffmpegPath string, ctrl scaletozero.Controller) (*SocketStreamer, error) {
	if params.FrameRate == nil || params.DisplayNum == nil {
		return nil, ErrInvalidParams
	}
	return &SocketStreamer{
		id:         id,
		params:     params,
		ffmpegPath: ffmpegPath,
		stz:        scaletozero.NewOncer(ctrl),
		clients:    make(map[WebSocketConn]struct{}),
	}, nil
}

func (s *SocketStreamer) ID() string {
	return s.id
}

func (s *SocketStreamer) Start(ctx context.Context) error {
	log := logger.FromContext(ctx)

	s.mu.Lock()
	if s.cmd != nil {
		s.mu.Unlock()
		return ErrStreamInProgress
	}
	if err := s.stz.Disable(ctx); err != nil {
		s.mu.Unlock()
		return err
	}

	videoInput, err := screenCaptureArgs(s.params)
	if err != nil {
		s.mu.Unlock()
		_ = s.stz.Enable(context.Background())
		return err
	}
	audioInput, err := audioCaptureArgs(ctx)
	if err != nil {
		s.mu.Unlock()
		_ = s.stz.Enable(context.Background())
		return err
	}

	args := append([]string{"-hide_banner", "-loglevel", "warning", "-nostdin"}, videoInput...)
	args = append(args, audioInput...)
	args = append(args,
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-pix_fmt", "yuv420p",
		"-g", strconv.Itoa(*s.params.FrameRate*2),
		"-map", "0:v:0",
		"-map", "1:a:0",
		"-c:a", "aac",
		"-b:a", "128k",
		"-ar", "44100",
		"-ac", "2",
		"-use_wallclock_as_timestamps", "1",
		"-fflags", "nobuffer",
		"-f", "mpegts",
		"pipe:1",
	)

	pr, pw := io.Pipe()
	cmd := exec.CommandContext(ctx, s.ffmpegPath, args...)
	cmd.Stdout = pw
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	exited := make(chan struct{})
	s.cmd = cmd
	s.exited = exited
	s.startedAt = time.Now()
	s.pr = pr
	s.pw = pw
	s.mu.Unlock()

	if err := cmd.Start(); err != nil {
		s.mu.Lock()
		s.cmd = nil
		s.exited = nil
		s.mu.Unlock()
		_ = s.stz.Enable(context.Background())
		return err
	}

	go s.broadcastLoop(pr)

	go func() {
		_ = cmd.Wait()
		close(exited)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.cmd = nil
		_ = s.stz.Enable(context.Background())
	}()

	// Detect immediate failures.
	select {
	case <-time.After(300 * time.Millisecond):
		log.Info("socket stream started", "id", s.id)
		return nil
	case <-exited:
		return ErrStreamStartFailed
	}
}

func (s *SocketStreamer) Stop(ctx context.Context) error {
	s.mu.Lock()
	cmd := s.cmd
	exited := s.exited
	pw := s.pw
	s.cmd = nil
	s.exited = nil
	s.pw = nil
	s.pr = nil
	s.mu.Unlock()

	if cmd != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	if pw != nil {
		_ = pw.Close()
	}

	s.mu.Lock()
	for c := range s.clients {
		_ = c.Close(int(websocket.StatusNormalClosure), "stream stopped")
	}
	s.clients = make(map[WebSocketConn]struct{})
	s.mu.Unlock()

	return s.stz.Enable(context.WithoutCancel(ctx))
}

func (s *SocketStreamer) IsStreaming(ctx context.Context) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil
}

func (s *SocketStreamer) Metadata() Metadata {
	s.mu.Lock()
	defer s.mu.Unlock()
	socketURL := ""
	// the HTTP handler fills the actual host, so we expose a relative path here
	url := "/stream/socket/" + s.id
	socketURL = url
	return Metadata{
		ID:           s.id,
		Mode:         ModeSocket,
		IngestURL:    "",
		PlaybackURL:  nil,
		StartedAt:    s.startedAt,
		WebsocketURL: &socketURL,
	}
}

func (s *SocketStreamer) RegisterClient(conn WebSocketConn) error {
	s.mu.Lock()
	if s.clients == nil {
		s.clients = make(map[WebSocketConn]struct{})
	}
	s.clients[conn] = struct{}{}
	s.mu.Unlock()

	// best-effort hint
	_ = conn.Write(context.Background(), websocket.MessageText, []byte("mpegts"))
	return nil
}

func (s *SocketStreamer) broadcastLoop(r io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			s.writeChunk(chunk)
		}
		if err != nil {
			return
		}
	}
}

func (s *SocketStreamer) writeChunk(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		if err := c.Write(context.Background(), websocket.MessageBinary, chunk); err != nil {
			_ = c.Close(int(websocket.StatusInternalError), "write failed")
			delete(s.clients, c)
		}
	}
}
