package api

import (
	"encoding/json"
	"net/http"
)

// GetClipboardHandler handles HTTP requests for GET /clipboard
func (s *ApiService) GetClipboardHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	result, err := s.GetClipboard(ctx, GetClipboardRequestObject{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response, ok := result.(GetClipboardResponseObject)
	if !ok {
		http.Error(w, "Internal server error: invalid response type", http.StatusInternalServerError)
		return
	}

	response.Visit(w)
}

// SetClipboardHandler handles HTTP requests for POST /clipboard
func (s *ApiService) SetClipboardHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var content ClipboardContent
	if err := json.NewDecoder(r.Body).Decode(&content); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	req := SetClipboardRequestObject{
		Body: content,
	}

	result, err := s.SetClipboard(ctx, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response, ok := result.(SetClipboardResponseObject)
	if !ok {
		http.Error(w, "Internal server error: invalid response type", http.StatusInternalServerError)
		return
	}

	response.Visit(w)
}

// StreamClipboardHandler handles HTTP requests for GET /clipboard/stream
func (s *ApiService) StreamClipboardHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	result, err := s.StreamClipboard(ctx, StreamClipboardRequestObject{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response, ok := result.(StreamClipboardResponseObject)
	if !ok {
		http.Error(w, "Internal server error: invalid response type", http.StatusInternalServerError)
		return
	}

	response.Visit(w)
}

// GetHealthHandler handles HTTP requests for GET /health
func (s *ApiService) GetHealthHandler(w http.ResponseWriter, r *http.Request) {
	// This is a simple health check endpoint
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "ok",
		"uptime_sec": 0, // Replace with actual uptime calculation
	})
}
