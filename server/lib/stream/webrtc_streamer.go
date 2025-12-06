package stream

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"github.com/onkernel/kernel-images/server/lib/logger"
	"github.com/onkernel/kernel-images/server/lib/scaletozero"
)

// WebRTCStreamer captures the display/audio and publishes them to WebRTC peers.
type WebRTCStreamer struct {
	mu sync.Mutex

	id         string
	params     Params
	ffmpegPath string

	videoTrack *webrtc.TrackLocalStaticRTP
	audioTrack *webrtc.TrackLocalStaticRTP

	videoConn *net.UDPConn
	audioConn *net.UDPConn

	cmd       *exec.Cmd
	exited    chan struct{}
	stz       *scaletozero.Oncer
	startedAt time.Time

	cancel context.CancelFunc
	peers  map[*webrtc.PeerConnection]struct{}
}

// NewWebRTCStreamer constructs a WebRTC streamer for the given params.
func NewWebRTCStreamer(id string, params Params, ffmpegPath string, ctrl scaletozero.Controller) (*WebRTCStreamer, error) {
	if params.FrameRate == nil || params.DisplayNum == nil {
		return nil, ErrInvalidParams
	}
	return &WebRTCStreamer{
		id:         id,
		params:     params,
		ffmpegPath: ffmpegPath,
		stz:        scaletozero.NewOncer(ctrl),
		peers:      make(map[*webrtc.PeerConnection]struct{}),
	}, nil
}

func (w *WebRTCStreamer) ID() string { return w.id }

func (w *WebRTCStreamer) Start(ctx context.Context) error {
	log := logger.FromContext(ctx)

	w.mu.Lock()
	if w.cmd != nil {
		w.mu.Unlock()
		return ErrStreamInProgress
	}
	if err := w.stz.Disable(ctx); err != nil {
		w.mu.Unlock()
		return err
	}

	videoTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000},
		"video", "display",
	)
	if err != nil {
		w.mu.Unlock()
		_ = w.stz.Enable(context.Background())
		return err
	}
	audioTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2},
		"audio", "display",
	)
	if err != nil {
		w.mu.Unlock()
		_ = w.stz.Enable(context.Background())
		return err
	}

	videoConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		w.mu.Unlock()
		_ = w.stz.Enable(context.Background())
		return err
	}
	audioConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		videoConn.Close()
		w.mu.Unlock()
		_ = w.stz.Enable(context.Background())
		return err
	}

	videoPort := videoConn.LocalAddr().(*net.UDPAddr).Port
	audioPort := audioConn.LocalAddr().(*net.UDPAddr).Port

	ctx, cancel := context.WithCancel(ctx)

	videoInput, err := screenCaptureArgs(w.params)
	if err != nil {
		cancel()
		w.mu.Unlock()
		_ = w.stz.Enable(context.Background())
		return err
	}
	audioInput, err := audioCaptureArgs(ctx)
	if err != nil {
		cancel()
		w.mu.Unlock()
		_ = w.stz.Enable(context.Background())
		return err
	}

	args := append([]string{"-hide_banner", "-loglevel", "warning", "-nostdin"}, videoInput...)
	args = append(args, audioInput...)
	args = append(args,
		"-c:v", "libvpx",
		"-b:v", "2M",
		"-g", strconv.Itoa(*w.params.FrameRate*2),
		"-pix_fmt", "yuv420p",
		"-map", "0:v:0",
		"-map", "1:a:0",
		"-c:a", "libopus",
		"-b:a", "128k",
		"-ar", "48000",
		"-ac", "2",
		"-f", "rtp",
		"-payload_type", "96",
		fmt.Sprintf("rtp://127.0.0.1:%d", videoPort),
		"-f", "rtp",
		"-payload_type", "111",
		fmt.Sprintf("rtp://127.0.0.1:%d", audioPort),
	)

	cmd := exec.CommandContext(ctx, w.ffmpegPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	exited := make(chan struct{})
	w.cmd = cmd
	w.exited = exited
	w.videoTrack = videoTrack
	w.audioTrack = audioTrack
	w.videoConn = videoConn
	w.audioConn = audioConn
	w.cancel = cancel
	w.startedAt = time.Now()
	w.mu.Unlock()

	if err := cmd.Start(); err != nil {
		w.mu.Lock()
		w.cmd = nil
		w.exited = nil
		w.videoConn.Close()
		w.audioConn.Close()
		w.mu.Unlock()
		cancel()
		_ = w.stz.Enable(context.Background())
		return err
	}

	go w.forwardRTP(ctx, videoConn, videoTrack)
	go w.forwardRTP(ctx, audioConn, audioTrack)

	go func() {
		_ = cmd.Wait()
		close(exited)
		cancel()
		w.mu.Lock()
		defer w.mu.Unlock()
		for pc := range w.peers {
			_ = pc.Close()
		}
		w.peers = make(map[*webrtc.PeerConnection]struct{})
		w.cmd = nil
		_ = w.stz.Enable(context.Background())
	}()

	select {
	case <-time.After(300 * time.Millisecond):
		log.Info("webrtc stream started", "id", w.id, "video_port", videoPort, "audio_port", audioPort)
		return nil
	case <-exited:
		return ErrStreamStartFailed
	}
}

func (w *WebRTCStreamer) Stop(ctx context.Context) error {
	w.mu.Lock()
	cmd := w.cmd
	exited := w.exited
	videoConn := w.videoConn
	audioConn := w.audioConn
	cancel := w.cancel
	w.cmd = nil
	w.exited = nil
	w.videoConn = nil
	w.audioConn = nil
	w.cancel = nil
	peers := w.peers
	w.peers = make(map[*webrtc.PeerConnection]struct{})
	w.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	for pc := range peers {
		_ = pc.Close()
	}
	if cmd != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	if videoConn != nil {
		_ = videoConn.Close()
	}
	if audioConn != nil {
		_ = audioConn.Close()
	}
	return w.stz.Enable(context.WithoutCancel(ctx))
}

func (w *WebRTCStreamer) IsStreaming(ctx context.Context) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cmd != nil
}

func (w *WebRTCStreamer) Metadata() Metadata {
	w.mu.Lock()
	defer w.mu.Unlock()
	offerURL := "/stream/webrtc/offer"
	return Metadata{
		ID:             w.id,
		Mode:           ModeWebRTC,
		IngestURL:      "",
		StartedAt:      w.startedAt,
		WebRTCOfferURL: &offerURL,
	}
}

// HandleOffer attaches a new peer to the running stream and returns the SDP answer.
func (w *WebRTCStreamer) HandleOffer(ctx context.Context, offer string) (string, error) {
	w.mu.Lock()
	if w.videoTrack == nil && w.audioTrack == nil {
		w.mu.Unlock()
		return "", fmt.Errorf("webrtc stream not ready")
	}
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		w.mu.Unlock()
		return "", err
	}
	if w.videoTrack != nil {
		if _, err := pc.AddTrack(w.videoTrack); err != nil {
			w.mu.Unlock()
			return "", err
		}
	}
	if w.audioTrack != nil {
		if _, err := pc.AddTrack(w.audioTrack); err != nil {
			w.mu.Unlock()
			return "", err
		}
	}
	w.peers[pc] = struct{}{}
	w.mu.Unlock()

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed || state == webrtc.PeerConnectionStateDisconnected {
			w.mu.Lock()
			delete(w.peers, pc)
			w.mu.Unlock()
			_ = pc.Close()
		}
	})

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		return "", err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return "", err
	}
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return "", err
	}
	select {
	case <-gatherComplete:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	local := pc.LocalDescription()
	if local == nil {
		return "", fmt.Errorf("local description missing")
	}
	return local.SDP, nil
}

func (w *WebRTCStreamer) forwardRTP(ctx context.Context, conn *net.UDPConn, track *webrtc.TrackLocalStaticRTP) {
	buf := make([]byte, 1500)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		var pkt rtp.Packet
		if err := pkt.Unmarshal(buf[:n]); err != nil {
			continue
		}
		if err := track.WriteRTP(&pkt); err != nil {
			return
		}
	}
}
