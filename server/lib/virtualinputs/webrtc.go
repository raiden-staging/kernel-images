package virtualinputs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media/ivfwriter"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
)

// WebRTCIngestor handles inbound WebRTC offers and writes received media to sinks.
// Video goes to the broadcaster (for the virtual feed page).
// Audio goes to PulseAudio via ffmpeg.
type WebRTCIngestor struct {
	mu     sync.Mutex
	config *webrtcIngestConfig

	pc     *webrtc.PeerConnection
	cancel context.CancelFunc

	videoSink io.Writer
	audioSink io.Writer
}

type webrtcIngestConfig struct {
	videoPath        string
	videoFormat      string
	audioPath        string
	audioFormat      string
	audioDestination AudioDestination
}

func NewWebRTCIngestor() *WebRTCIngestor {
	return &WebRTCIngestor{}
}

// Configure sets the target formats for subsequent WebRTC offers.
// Note: paths are kept for API compatibility but video goes directly to sink,
// and audio goes to PulseAudio via ffmpeg.
// audioDestination specifies where to route audio: "microphone" (default) or "speaker".
func (w *WebRTCIngestor) Configure(videoPath, videoFormat, audioPath, audioFormat string, audioDestination AudioDestination) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if audioDestination == "" {
		audioDestination = AudioDestinationMicrophone // default
	}
	w.config = &webrtcIngestConfig{
		videoPath:        videoPath,
		videoFormat:      videoFormat,
		audioPath:        audioPath,
		audioFormat:      audioFormat,
		audioDestination: audioDestination,
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

// SetSinks sets the writers for incoming media.
// Video sink is required for the virtual feed broadcaster.
// Audio sink is optional (audio goes to PulseAudio via ffmpeg).
func (w *WebRTCIngestor) SetSinks(video io.Writer, audio io.Writer) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.videoSink = video
	w.audioSink = audio
}

// HandleOffer negotiates a PeerConnection and starts forwarding tracks.
func (w *WebRTCIngestor) HandleOffer(ctx context.Context, offerSDP string) (string, error) {
	runCtx := context.WithoutCancel(ctx)

	w.mu.Lock()
	cfg := w.config
	if cfg == nil {
		w.mu.Unlock()
		return "", fmt.Errorf("webrtc ingest not configured")
	}
	// We need either video or audio configured
	hasVideo := cfg.videoPath != "" || cfg.videoFormat != ""
	hasAudio := cfg.audioPath != "" || cfg.audioFormat != ""
	if !hasVideo && !hasAudio {
		w.mu.Unlock()
		return "", fmt.Errorf("webrtc ingest not configured for video or audio")
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
	videoSink := w.videoSink
	w.mu.Unlock()

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateClosed {
			cancel()
		}
	})

	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		switch track.Kind() {
		case webrtc.RTPCodecTypeVideo:
			if !hasVideo {
				return
			}
			_ = w.forwardVideo(ctx, cfg, track, videoSink)
		case webrtc.RTPCodecTypeAudio:
			if !hasAudio {
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

// forwardVideo writes incoming video RTP packets as IVF to the video sink (broadcaster).
func (w *WebRTCIngestor) forwardVideo(ctx context.Context, cfg *webrtcIngestConfig, track *webrtc.TrackRemote, videoSink io.Writer) error {
	if cfg.videoFormat != "" && cfg.videoFormat != "ivf" {
		return fmt.Errorf("unsupported video format %s", cfg.videoFormat)
	}
	if track.Codec().MimeType != webrtc.MimeTypeVP8 && track.Codec().MimeType != webrtc.MimeTypeVP9 {
		return fmt.Errorf("unsupported video codec %s", track.Codec().MimeType)
	}

	if videoSink == nil {
		return fmt.Errorf("video sink not configured")
	}

	writer, err := ivfwriter.NewWith(videoSink)
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

// forwardAudio pipes incoming audio RTP packets through ffmpeg to PulseAudio.
// The destination determines whether audio goes to virtual mic (audio_input) or speaker (audio_output).
func (w *WebRTCIngestor) forwardAudio(ctx context.Context, cfg *webrtcIngestConfig, track *webrtc.TrackRemote) error {
	if cfg.audioFormat != "" && cfg.audioFormat != "ogg" {
		return fmt.Errorf("unsupported audio format %s", cfg.audioFormat)
	}
	if track.Codec().MimeType != webrtc.MimeTypeOpus {
		return fmt.Errorf("unsupported audio codec %s", track.Codec().MimeType)
	}

	// Determine the PulseAudio sink based on destination
	sink := "audio_input" // default: virtual microphone
	if cfg.audioDestination == AudioDestinationSpeaker {
		sink = "audio_output"
	}

	// Create a pipe to connect oggwriter output to ffmpeg input
	pr, pw := io.Pipe()

	// Start ffmpeg to decode OGG/Opus and output to PulseAudio
	args := []string{
		"-hide_banner", "-loglevel", "warning",
		"-f", "ogg",
		"-i", "pipe:0",
		"-ac", "2",
		"-ar", "48000",
		"-f", "pulse",
		sink,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdin = pr

	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return fmt.Errorf("failed to start ffmpeg for audio: %w", err)
	}

	// Clean up ffmpeg when done
	go func() {
		_ = cmd.Wait()
	}()

	// Create OGG writer that writes to the pipe
	writer, err := oggwriter.NewWith(pw, track.Codec().ClockRate, track.Codec().Channels)
	if err != nil {
		pw.Close()
		pr.Close()
		return fmt.Errorf("create ogg writer: %w", err)
	}

	defer func() {
		writer.Close()
		pw.Close()
		pr.Close()
	}()

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
