package api

import (
	"net/http"

	"github.com/coder/websocket"

	"github.com/onkernel/kernel-images/server/lib/logger"
	"github.com/onkernel/kernel-images/server/lib/virtualinputs"
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

	for {
		msgType, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		if msgType != websocket.MessageBinary {
			continue
		}
		if len(data) == 0 {
			continue
		}
		if _, err := pipe.Write(data); err != nil {
			log.Error("failed writing websocket chunk to pipe", "err", err, "kind", kind)
			return
		}
	}
}
