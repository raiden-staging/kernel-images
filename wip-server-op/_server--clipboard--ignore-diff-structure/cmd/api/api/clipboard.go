package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/onkernel/kernel-images/server/lib/logger"
)

// ClipboardManager handles clipboard operations
type ClipboardManager struct {
	mu                sync.Mutex
	lastClipboardText string
	display           string
}

// NewClipboardManager creates a new clipboard manager
func NewClipboardManager() *ClipboardManager {
	display := os.Getenv("DISPLAY")
	if display == "" {
		display = ":20" // Default display for testing environment
	}

	return &ClipboardManager{
		display: display,
	}
}

// ClipboardContent represents clipboard content
type ClipboardContent struct {
	Type      string  `json:"type"`
	Text      *string `json:"text,omitempty"`
	ImageB64  *string `json:"image_b64,omitempty"`
	ImageMime *string `json:"image_mime,omitempty"`
}

// ClipboardSetResponse represents the response to setting clipboard content
type ClipboardSetResponse struct {
	Ok      bool    `json:"ok"`
	Message *string `json:"message,omitempty"`
}

// ClipboardEvent represents a clipboard change event
type ClipboardEvent struct {
	Ts      string  `json:"ts"`
	Type    string  `json:"type"`
	Preview *string `json:"preview,omitempty"`
}

// GetClipboard retrieves the current clipboard content using xclip or wl-paste
func (cm *ClipboardManager) GetClipboard(ctx context.Context) (*ClipboardContent, error) {
	log := logger.FromContext(ctx)

	// Try xclip first
	content, err := cm.getClipboardXClip(ctx)
	if err == nil {
		return content, nil
	}

	log.Warn("xclip failed, trying wayland clipboard", "err", err)

	// Fall back to wayland (wl-paste)
	content, err = cm.getClipboardWayland(ctx)
	if err != nil {
		log.Error("all clipboard methods failed", "err", err)
		// Return empty text as fallback
		return &ClipboardContent{
			Type: "text",
			Text: stringPtr(""),
		}, nil
	}

	return content, nil
}

// SetClipboard sets clipboard content using xclip or wl-copy
func (cm *ClipboardManager) SetClipboard(ctx context.Context, content *ClipboardContent) error {
	log := logger.FromContext(ctx)

	// Try xclip first
	err := cm.setClipboardXClip(ctx, content)
	if err == nil {
		return nil
	}

	log.Warn("xclip set failed, trying wayland clipboard", "err", err)

	// Fall back to wayland (wl-copy)
	err = cm.setClipboardWayland(ctx, content)
	if err != nil {
		log.Error("all clipboard methods failed", "err", err)
		return fmt.Errorf("failed to set clipboard: %w", err)
	}

	return nil
}

// getClipboardXClip gets clipboard content using xclip
func (cm *ClipboardManager) getClipboardXClip(ctx context.Context) (*ClipboardContent, error) {
	log := logger.FromContext(ctx)
	log.Debug("attempting to get clipboard content using xclip")

	cmd := exec.CommandContext(ctx, "bash", "-lc", fmt.Sprintf("DISPLAY=%s xclip -selection clipboard -o 2>/dev/null || true", cm.display))
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("xclip failed: %w", err)
	}

	// For now, we only support text with xclip
	return &ClipboardContent{
		Type: "text",
		Text: stringPtr(string(out)),
	}, nil
}

// getClipboardWayland gets clipboard content using wl-paste
func (cm *ClipboardManager) getClipboardWayland(ctx context.Context) (*ClipboardContent, error) {
	log := logger.FromContext(ctx)
	log.Debug("attempting to get clipboard content using wl-paste")

	// Try text first
	cmdText := exec.CommandContext(ctx, "bash", "-lc", "wl-paste -t text 2>/dev/null || true")
	textOut, err := cmdText.Output()
	if err == nil && len(textOut) > 0 {
		return &ClipboardContent{
			Type: "text",
			Text: stringPtr(string(textOut)),
		}, nil
	}

	// Try image
	cmdImage := exec.CommandContext(ctx, "bash", "-lc", "wl-paste -t image/png | base64 -w0 2>/dev/null || true")
	imgOut, err := cmdImage.Output()
	if err == nil && len(imgOut) > 0 {
		return &ClipboardContent{
			Type:      "image",
			ImageB64:  stringPtr(string(imgOut)),
			ImageMime: stringPtr("image/png"),
		}, nil
	}

	return nil, fmt.Errorf("no clipboard content available via wayland")
}

// setClipboardXClip sets clipboard content using xclip
func (cm *ClipboardManager) setClipboardXClip(ctx context.Context, content *ClipboardContent) error {
	log := logger.FromContext(ctx)
	log.Debug("setting clipboard with xclip", "type", content.Type)

	if content.Type != "text" {
		return fmt.Errorf("only text clipboard type supported with xclip")
	}

	if content.Text == nil {
		content.Text = stringPtr("")
	}

	// Escape the text for shell
	jsonEscaped := strings.ReplaceAll(*content.Text, `"`, `\"`)

	cmd := exec.CommandContext(ctx, "bash", "-lc",
		fmt.Sprintf(`printf "%%s" "%s" | DISPLAY=%s xclip -selection clipboard`,
			jsonEscaped, cm.display))

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("xclip set failed: %w", err)
	}

	return nil
}

// setClipboardWayland sets clipboard content using wl-copy
func (cm *ClipboardManager) setClipboardWayland(ctx context.Context, content *ClipboardContent) error {
	log := logger.FromContext(ctx)
	log.Debug("setting clipboard with wayland", "type", content.Type)

	if content.Type == "text" {
		if content.Text == nil {
			content.Text = stringPtr("")
		}

		// Escape the text for shell
		jsonEscaped := strings.ReplaceAll(*content.Text, `"`, `\"`)

		cmd := exec.CommandContext(ctx, "bash", "-lc",
			fmt.Sprintf(`printf "%%s" "%s" | wl-copy`, jsonEscaped))

		err := cmd.Run()
		if err != nil {
			return fmt.Errorf("wl-copy text set failed: %w", err)
		}
	} else if content.Type == "image" && content.ImageB64 != nil {
		mime := "image/png"
		if content.ImageMime != nil {
			mime = *content.ImageMime
		}

		imgData, err := base64.StdEncoding.DecodeString(*content.ImageB64)
		if err != nil {
			return fmt.Errorf("invalid base64 image: %w", err)
		}

		cmd := exec.CommandContext(ctx, "bash", "-lc",
			fmt.Sprintf("wl-copy -t %s", mime))

		stdin, err := cmd.StdinPipe()
		if err != nil {
			return fmt.Errorf("failed to get stdin pipe: %w", err)
		}

		go func() {
			defer stdin.Close()
			io.Copy(stdin, bytes.NewReader(imgData))
		}()

		err = cmd.Run()
		if err != nil {
			return fmt.Errorf("wl-copy image set failed: %w", err)
		}
	} else {
		return fmt.Errorf("invalid clipboard content")
	}

	return nil
}

// streamClipboard watches for clipboard changes and returns events via a channel
func (cm *ClipboardManager) streamClipboard(ctx context.Context) (<-chan ClipboardEvent, error) {
	eventCh := make(chan ClipboardEvent)

	go func() {
		defer close(eventCh)

		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		var lastContent string

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				content, err := cm.GetClipboard(ctx)
				if err != nil {
					continue
				}

				var currContent string
				if content.Type == "text" && content.Text != nil {
					currContent = *content.Text
				} else if content.Type == "image" && content.ImageB64 != nil {
					if len(*content.ImageB64) > 16 {
						currContent = (*content.ImageB64)[:16]
					} else {
						currContent = *content.ImageB64
					}
				}

				if currContent != lastContent {
					lastContent = currContent

					var preview string
					if content.Type == "text" && content.Text != nil {
						if len(*content.Text) > 100 {
							preview = (*content.Text)[:100]
						} else {
							preview = *content.Text
						}
					} else {
						preview = "image..."
					}

					eventCh <- ClipboardEvent{
						Ts:      time.Now().Format(time.RFC3339),
						Type:    content.Type,
						Preview: &preview,
					}
				}
			}
		}
	}()

	return eventCh, nil
}

// stringPtr returns a pointer to the string value
func stringPtr(s string) *string {
	return &s
}

// GetClipboardRequestObject defines the request for GetClipboard
type GetClipboardRequestObject struct{}

// GetClipboardResponseObject defines the response for GetClipboard
type GetClipboardResponseObject interface {
	Visit(w http.ResponseWriter) error
}

// GetClipboard200JSONResponse is the successful response for GetClipboard
type GetClipboard200JSONResponse ClipboardContent

// Visit writes the response to the HTTP response writer
func (resp GetClipboard200JSONResponse) Visit(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(resp)
}

// GetClipboard500JSONResponse is the error response for GetClipboard
type GetClipboard500JSONResponse struct {
	Message string `json:"message"`
}

// Visit writes the response to the HTTP response writer
func (resp GetClipboard500JSONResponse) Visit(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	return json.NewEncoder(w).Encode(resp)
}

// SetClipboardRequestObject defines the request for SetClipboard
type SetClipboardRequestObject struct {
	Body ClipboardContent
}

// SetClipboardResponseObject defines the response for SetClipboard
type SetClipboardResponseObject interface {
	Visit(w http.ResponseWriter) error
}

// SetClipboard200JSONResponse is the successful response for SetClipboard
type SetClipboard200JSONResponse ClipboardSetResponse

// Visit writes the response to the HTTP response writer
func (resp SetClipboard200JSONResponse) Visit(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(resp)
}

// SetClipboard400JSONResponse is the bad request response for SetClipboard
type SetClipboard400JSONResponse struct {
	Message string `json:"message"`
}

// Visit writes the response to the HTTP response writer
func (resp SetClipboard400JSONResponse) Visit(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	return json.NewEncoder(w).Encode(resp)
}

// SetClipboard500JSONResponse is the error response for SetClipboard
type SetClipboard500JSONResponse struct {
	Message string `json:"message"`
}

// Visit writes the response to the HTTP response writer
func (resp SetClipboard500JSONResponse) Visit(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	return json.NewEncoder(w).Encode(resp)
}

// StreamClipboardRequestObject defines the request for StreamClipboard
type StreamClipboardRequestObject struct{}

// StreamClipboardResponseObject defines the response for StreamClipboard
type StreamClipboardResponseObject interface {
	Visit(w http.ResponseWriter) error
}

// StreamClipboard200SSEResponse is the successful response for StreamClipboard
type StreamClipboard200SSEResponse struct {
	Stream io.ReadCloser
}

// Visit writes the response to the HTTP response writer
func (resp StreamClipboard200SSEResponse) Visit(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-SSE-Content-Type", "application/json")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported")
	}

	io.Copy(w, resp.Stream)
	flusher.Flush()
	return nil
}

// GetClipboard implements the GET /clipboard endpoint
func (s *ApiService) GetClipboard(ctx context.Context, _ GetClipboardRequestObject) (GetClipboardResponseObject, error) {
	log := logger.FromContext(ctx)
	log.Info("GET /clipboard request received")

	if s.clipboardManager == nil {
		s.clipboardManager = NewClipboardManager()
	}

	content, err := s.clipboardManager.GetClipboard(ctx)
	if err != nil {
		log.Error("Error getting clipboard content", "err", err)
		return GetClipboard500JSONResponse{
			Message: "Failed to get clipboard content",
		}, nil
	}

	return GetClipboard200JSONResponse(*content), nil
}

// SetClipboard implements the POST /clipboard endpoint
func (s *ApiService) SetClipboard(ctx context.Context, req SetClipboardRequestObject) (SetClipboardResponseObject, error) {
	log := logger.FromContext(ctx)
	log.Info("POST /clipboard request received")

	if s.clipboardManager == nil {
		s.clipboardManager = NewClipboardManager()
	}

	content := &req.Body

	if content.Type != "text" && content.Type != "image" {
		return SetClipboard400JSONResponse{
			Message: "Invalid clipboard type",
		}, nil
	}

	err := s.clipboardManager.SetClipboard(ctx, content)
	if err != nil {
		log.Error("Error setting clipboard content", "err", err)
		return SetClipboard500JSONResponse{
			Message: fmt.Sprintf("Failed to set clipboard: %v", err),
		}, nil
	}

	return SetClipboard200JSONResponse{
		Ok: true,
	}, nil
}

// PipeReader adapts a channel of events to an io.ReadCloser
type PipeReader struct {
	events  <-chan ClipboardEvent
	ctx     context.Context
	cancel  context.CancelFunc
	buf     bytes.Buffer
	closed  bool
	closing chan struct{}
}

// NewPipeReader creates a new PipeReader
func NewPipeReader(ctx context.Context, events <-chan ClipboardEvent) *PipeReader {
	ctx, cancel := context.WithCancel(ctx)
	pr := &PipeReader{
		events:  events,
		ctx:     ctx,
		cancel:  cancel,
		closing: make(chan struct{}),
	}

	// Start the reader goroutine
	go pr.readEvents()

	return pr
}

// readEvents reads events from the channel and writes them to the buffer
func (pr *PipeReader) readEvents() {
	defer close(pr.closing)

	for {
		select {
		case <-pr.ctx.Done():
			return
		case event, ok := <-pr.events:
			if !ok {
				return
			}

			eventJSON, err := json.Marshal(event)
			if err != nil {
				continue
			}

			sseData := fmt.Sprintf("event: data\ndata: %s\n\n", string(eventJSON))

			pr.buf.WriteString(sseData)
		}
	}
}

// Read implements io.Reader
func (pr *PipeReader) Read(p []byte) (n int, err error) {
	if pr.closed {
		return 0, io.EOF
	}

	// If there's data in the buffer, return it
	if pr.buf.Len() > 0 {
		return pr.buf.Read(p)
	}

	// Wait for more data or close
	select {
	case <-pr.ctx.Done():
		return 0, io.EOF
	case <-pr.closing:
		return 0, io.EOF
	case <-time.After(100 * time.Millisecond):
		// Check again for data
		if pr.buf.Len() > 0 {
			return pr.buf.Read(p)
		}
		return 0, nil
	}
}

// Close implements io.Closer
func (pr *PipeReader) Close() error {
	if pr.closed {
		return nil
	}
	pr.closed = true
	pr.cancel()
	return nil
}

// StreamClipboard implements the GET /clipboard/stream endpoint
func (s *ApiService) StreamClipboard(ctx context.Context, _ StreamClipboardRequestObject) (StreamClipboardResponseObject, error) {
	log := logger.FromContext(ctx)
	log.Info("GET /clipboard/stream request received")

	if s.clipboardManager == nil {
		s.clipboardManager = NewClipboardManager()
	}

	events, err := s.clipboardManager.streamClipboard(ctx)
	if err != nil {
		log.Error("Error starting clipboard stream", "err", err)
		return nil, err
	}

	stream := NewPipeReader(ctx, events)

	return StreamClipboard200SSEResponse{
		Stream: stream,
	}, nil
}
