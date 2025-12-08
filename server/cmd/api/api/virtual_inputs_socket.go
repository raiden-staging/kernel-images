package api

import (
	"io"
	"net/http"

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

// HandleVirtualInputAudioSocket upgrades the connection and streams binary chunks into the audio FIFO.
func (s *ApiService) HandleVirtualInputAudioSocket(w http.ResponseWriter, r *http.Request) {
	s.handleVirtualInputSocket(w, r, "audio")
}

// HandleVirtualInputVideoSocket upgrades the connection and streams binary chunks into the video FIFO.
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

	if endpoint == nil || endpoint.Protocol != "socket" || endpoint.Path == "" {
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

	pipe, err := virtualinputs.OpenPipeWriter(endpoint.Path, virtualinputs.DefaultPipeOpenTimeout)
	if err != nil {
		log.Error("failed to open ingest pipe", "err", err, "path", endpoint.Path)
		_ = conn.Close(websocket.StatusInternalError, "pipe unavailable")
		return
	}
	defer pipe.Close()

	// send a tiny hint to the client about the expected format
	if endpoint.Format != "" {
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(endpoint.Format))
	}

	var broadcaster *virtualFeedBroadcaster
	format := ""
	if kind == "video" {
		if s.virtualFeed == nil {
			log.Warn("virtual feed not available for websocket broadcasting")
		} else {
			broadcaster = s.virtualFeed
			format = endpoint.Format
			if format == "" {
				format = "mpegts"
			}
		}
	}

	buf := make([]byte, socketChunkSize)
	for {
		msgType, reader, err := conn.Reader(r.Context())
		if err != nil {
			log.Info("virtual input socket closed", "kind", kind, "err", err)
			return
		}
		if msgType != websocket.MessageBinary {
			_, _ = io.Copy(io.Discard, reader)
			continue
		}

		written, chunks, err := writeChunkedToPipe(reader, pipe, broadcaster, format, buf)
		if err != nil {
			log.Error("failed writing websocket chunk to pipe", "err", err, "kind", kind)
			return
		}
		log.Info("received websocket chunk", "kind", kind, "len", written, "chunks", chunks)
	}
}

// writeChunkedToPipe slices websocket payloads into pipe-friendly segments, preserving order
// and mirroring video chunks to the virtual feed when requested.
func writeChunkedToPipe(src io.Reader, pipe io.Writer, broadcaster *virtualFeedBroadcaster, format string, buf []byte) (int64, int, error) {
	var (
		totalWritten int64
		chunks       int
	)
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			remaining := buf[:n]
			for len(remaining) > 0 {
				written, err := pipe.Write(remaining)
				if err != nil {
					return totalWritten, chunks, err
				}
				if broadcaster != nil && written > 0 {
					broadcaster.broadcastWithFormat(format, remaining[:written])
				}

				totalWritten += int64(written)
				chunks++
				if written == 0 {
					return totalWritten, chunks, io.ErrShortWrite
				}
				remaining = remaining[written:]
			}
		}

		if readErr != nil {
			if readErr == io.EOF {
				return totalWritten, chunks, nil
			}
			return totalWritten, chunks, readErr
		}
	}
}
