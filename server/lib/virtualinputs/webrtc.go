package virtualinputs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media/ivfwriter"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
)

// WebRTCIngestor handles inbound WebRTC offers and writes received media into named pipes that
// the FFmpeg pipeline already consumes.
type WebRTCIngestor struct {
	mu     sync.Mutex
	config *webrtcIngestConfig

	pc     *webrtc.PeerConnection
	cancel context.CancelFunc

	videoSink io.Writer
	audioSink io.Writer
}

type webrtcIngestConfig struct {
	videoPath   string
	videoFormat string
	audioPath   string
	audioFormat string
}

func NewWebRTCIngestor() *WebRTCIngestor {
	return &WebRTCIngestor{}
}

// Configure sets the target pipes/formats for subsequent WebRTC offers.
func (w *WebRTCIngestor) Configure(videoPath, videoFormat, audioPath, audioFormat string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.config = &webrtcIngestConfig{
		videoPath:   videoPath,
		videoFormat: videoFormat,
		audioPath:   audioPath,
		audioFormat: audioFormat,
	}
}

// Clear tears down any active connection and removes the configured targets.
func (w *WebRTCIngestor) Clear() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	if w.pc != nil {
		_ = w.pc.Close()
		w.pc = nil
	}
	w.config = nil
}

// SetSinks sets optional mirror writers for incoming media.
func (w *WebRTCIngestor) SetSinks(video io.Writer, audio io.Writer) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.videoSink = video
	w.audioSink = audio
}

// HandleOffer negotiates a PeerConnection and starts forwarding tracks into the configured pipes.
func (w *WebRTCIngestor) HandleOffer(ctx context.Context, offerSDP string) (string, error) {
	runCtx := context.WithoutCancel(ctx)

	w.mu.Lock()
	cfg := w.config
	if cfg == nil {
		w.mu.Unlock()
		return "", fmt.Errorf("webrtc ingest not configured")
	}
	if cfg.videoPath == "" && cfg.audioPath == "" {
		w.mu.Unlock()
		return "", fmt.Errorf("webrtc ingest paths not configured")
	}
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	if w.pc != nil {
		_ = w.pc.Close()
		w.pc = nil
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		w.mu.Unlock()
		return "", fmt.Errorf("failed to create peerconnection: %w", err)
	}
	ctx, cancel := context.WithCancel(runCtx)
	w.pc = pc
	w.cancel = cancel
	w.mu.Unlock()

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateClosed ||
			state == webrtc.PeerConnectionStateDisconnected {
			cancel()
		}
	})

	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		switch track.Kind() {
		case webrtc.RTPCodecTypeVideo:
			if cfg.videoPath == "" {
				return
			}
			_ = w.forwardVideo(ctx, cfg, track)
		case webrtc.RTPCodecTypeAudio:
			if cfg.audioPath == "" {
				return
			}
			_ = w.forwardAudio(ctx, cfg, track)
		default:
		}
	})

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{SDP: offerSDP, Type: webrtc.SDPTypeOffer}); err != nil {
		cancel()
		return "", fmt.Errorf("set remote description: %w", err)
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		cancel()
		return "", fmt.Errorf("create answer: %w", err)
	}
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		cancel()
		return "", fmt.Errorf("set local description: %w", err)
	}
	<-gatherComplete

	local := pc.LocalDescription()
	if local == nil {
		cancel()
		return "", fmt.Errorf("local description missing")
	}
	return local.SDP, nil
}

func (w *WebRTCIngestor) forwardVideo(ctx context.Context, cfg *webrtcIngestConfig, track *webrtc.TrackRemote) error {
	if cfg.videoFormat != "" && cfg.videoFormat != "ivf" {
		return fmt.Errorf("unsupported video format %s", cfg.videoFormat)
	}
	if track.Codec().MimeType != webrtc.MimeTypeVP8 && track.Codec().MimeType != webrtc.MimeTypeVP9 {
		return fmt.Errorf("unsupported video codec %s", track.Codec().MimeType)
	}

	out, err := OpenPipeWriter(cfg.videoPath, DefaultPipeOpenTimeout)
	if err != nil {
		return err
	}
	defer out.Close() // best-effort on exit

	w.mu.Lock()
	videoSink := w.videoSink
	w.mu.Unlock()

	target := io.Writer(out)
	if videoSink != nil {
		target = io.MultiWriter(out, videoSink)
	}

	writer, err := ivfwriter.NewWith(target)
	if err != nil {
		return fmt.Errorf("create ivf writer: %w", err)
	}
	defer writer.Close()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		pkt, _, err := track.ReadRTP()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		if err := writer.WriteRTP(pkt); err != nil {
			return err
		}
	}
}

func (w *WebRTCIngestor) forwardAudio(ctx context.Context, cfg *webrtcIngestConfig, track *webrtc.TrackRemote) error {
	if cfg.audioFormat != "" && cfg.audioFormat != "ogg" {
		return fmt.Errorf("unsupported audio format %s", cfg.audioFormat)
	}
	if track.Codec().MimeType != webrtc.MimeTypeOpus {
		return fmt.Errorf("unsupported audio codec %s", track.Codec().MimeType)
	}

	out, err := OpenPipeWriter(cfg.audioPath, DefaultPipeOpenTimeout)
	if err != nil {
		return err
	}
	defer out.Close()

	w.mu.Lock()
	audioSink := w.audioSink
	w.mu.Unlock()

	target := io.Writer(out)
	if audioSink != nil {
		target = io.MultiWriter(out, audioSink)
	}

	writer, err := oggwriter.NewWith(target, track.Codec().ClockRate, track.Codec().Channels)
	if err != nil {
		return fmt.Errorf("create ogg writer: %w", err)
	}
	defer writer.Close()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		pkt, _, err := track.ReadRTP()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		if err := writer.WriteRTP(pkt); err != nil {
			return err
		}
	}
}
