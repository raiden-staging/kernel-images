package api

import (
	"io"
	"log/slog"
	"net/http"
	"os/exec"

	"github.com/coder/websocket"

	"github.com/onkernel/kernel-images/server/lib/logger"
	"github.com/onkernel/kernel-images/server/lib/virtualinputs"
)

const (
	socketChunkSize = 64 * 1024
	// Lift the default websocket read cap so clients can deliver larger payloads
	// while still encouraging chunked writes.
	socketReadLimit = 64 * 1024 * 1024
)

// HandleVirtualInputAudioSocket upgrades the connection and streams binary chunks to PulseAudio.
func (s *ApiService) HandleVirtualInputAudioSocket(w http.ResponseWriter, r *http.Request) {
	s.handleVirtualInputSocket(w, r, "audio")
}

// HandleVirtualInputVideoSocket upgrades the connection and streams binary chunks to the virtual feed broadcaster.
func (s *ApiService) HandleVirtualInputVideoSocket(w http.ResponseWriter, r *http.Request) {
	s.handleVirtualInputSocket(w, r, "video")
}

func (s *ApiService) handleVirtualInputSocket(w http.ResponseWriter, r *http.Request, kind string) {
	log := logger.FromContext(r.Context())
	log.Info("virtual input socket connection", "kind", kind)
	status := s.virtualInputs.Status(r.Context())
	var endpoint *virtualinputs.IngestEndpoint
	switch kind {
	case "audio":
		if status.Ingest != nil {
			endpoint = status.Ingest.Audio
		}
	case "video":
		if status.Ingest != nil {
			endpoint = status.Ingest.Video
		}
	}

	if endpoint == nil || endpoint.Protocol != "socket" {
		http.Error(w, "socket ingest not configured", http.StatusConflict)
		return
	}

	s.socketMu.Lock()
	active := (kind == "audio" && s.audioSocketActive) || (kind == "video" && s.videoSocketActive)
	if active {
		s.socketMu.Unlock()
		http.Error(w, "socket ingest already connected", http.StatusConflict)
		return
	}
	if kind == "audio" {
		s.audioSocketActive = true
	} else {
		s.videoSocketActive = true
	}
	s.socketMu.Unlock()
	defer func() {
		s.socketMu.Lock()
		if kind == "audio" {
			s.audioSocketActive = false
		} else {
			s.videoSocketActive = false
		}
		s.socketMu.Unlock()
	}()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionNoContextTakeover,
	})
	if err != nil {
		log.Error("failed to accept websocket for virtual input", "err", err, "kind", kind)
		return
	}
	conn.SetReadLimit(socketReadLimit)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	// send a tiny hint to the client about the expected format
	if endpoint.Format != "" {
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(endpoint.Format))
	}

	format := endpoint.Format
	if format == "" {
		if kind == "video" {
			format = "mpegts"
		} else {
			format = "mp3"
		}
	}

	if kind == "video" {
		// Video goes directly to the broadcaster - no pipe/FFmpeg needed
		s.handleVideoSocketIngest(r, conn, log, format)
	} else {
		// Audio goes to PulseAudio virtual microphone via ffmpeg
		s.handleAudioSocketIngest(r, conn, log, format)
	}
}

// handleVideoSocketIngest streams video chunks directly to the virtual feed broadcaster.
// The broadcaster fans out to all connected websocket clients watching the feed page.
func (s *ApiService) handleVideoSocketIngest(r *http.Request, conn *websocket.Conn, log *slog.Logger, format string) {
	if s.virtualFeed == nil {
		log.Error("virtual feed broadcaster not available")
		_ = conn.Close(websocket.StatusInternalError, "broadcaster unavailable")
		return
	}

	// Set the format on the broadcaster so clients know what to expect
	s.virtualFeed.setFormat(format)

	buf := make([]byte, socketChunkSize)
	for {
		msgType, reader, err := conn.Reader(r.Context())
		if err != nil {
			log.Info("virtual input video socket closed", "err", err)
			return
		}
		if msgType != websocket.MessageBinary {
			_, _ = io.Copy(io.Discard, reader)
			continue
		}

		// Read and broadcast directly - no pipe intermediary
		written, chunks := broadcastFromReader(reader, s.virtualFeed, format, buf)
		log.Info("received video websocket chunk", "len", written, "chunks", chunks)
	}
}

// handleAudioSocketIngest streams audio chunks to PulseAudio via ffmpeg.
// This creates a long-running ffmpeg process that decodes the incoming audio format
// and outputs to the virtual microphone sink.
func (s *ApiService) handleAudioSocketIngest(r *http.Request, conn *websocket.Conn, log *slog.Logger, format string) {
	// Start ffmpeg to decode incoming audio and pipe to PulseAudio
	// ffmpeg -f mp3 -i pipe:0 -f pulse audio_input
	args := []string{
		"-hide_banner", "-loglevel", "warning",
		"-f", format,
		"-i", "pipe:0",
		"-ac", "2",
		"-ar", "48000",
		"-f", "pulse",
		"audio_input",
	}

	cmd := exec.CommandContext(r.Context(), "ffmpeg", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Error("failed to create ffmpeg stdin pipe", "err", err)
		_ = conn.Close(websocket.StatusInternalError, "ffmpeg setup failed")
		return
	}

	if err := cmd.Start(); err != nil {
		log.Error("failed to start ffmpeg for audio ingest", "err", err)
		_ = conn.Close(websocket.StatusInternalError, "ffmpeg start failed")
		return
	}

	// Ensure ffmpeg is cleaned up when we're done
	defer func() {
		stdin.Close()
		_ = cmd.Wait()
	}()

	buf := make([]byte, socketChunkSize)
	for {
		msgType, reader, err := conn.Reader(r.Context())
		if err != nil {
			log.Info("virtual input audio socket closed", "err", err)
			return
		}
		if msgType != websocket.MessageBinary {
			_, _ = io.Copy(io.Discard, reader)
			continue
		}

		// Write audio data to ffmpeg's stdin
		written, err := io.CopyBuffer(stdin, reader, buf)
		if err != nil {
			log.Error("failed writing audio to ffmpeg", "err", err)
			return
		}
		log.Debug("received audio websocket chunk", "len", written)
	}
}

// broadcastFromReader reads from src and broadcasts chunks to the virtual feed.
func broadcastFromReader(src io.Reader, broadcaster *virtualFeedBroadcaster, format string, buf []byte) (int64, int) {
	var (
		totalWritten int64
		chunks       int
	)
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			broadcaster.broadcastWithFormat(format, buf[:n])
			totalWritten += int64(n)
			chunks++
		}

		if readErr != nil {
			return totalWritten, chunks
		}
	}
}
