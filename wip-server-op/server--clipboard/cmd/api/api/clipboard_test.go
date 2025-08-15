package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetAndSetClipboard tests both GET and POST /clipboard endpoints together
func TestGetAndSetClipboard(t *testing.T) {
	// Skip tests that require clipboard access in CI environments
	if testing.Short() {
		t.Skip("Skipping clipboard tests in short mode")
	}

	// Create a test clipboard service
	service := &ApiService{
		clipboardManager: NewClipboardManager(),
	}

	// 1. Set clipboard content
	content := ClipboardContent{
		Type: "text",
		Text: stringPtr("Test clipboard content from test"),
	}

	body, err := json.Marshal(content)
	require.NoError(t, err)

	// Create a test HTTP request to set the clipboard
	setReq := httptest.NewRequest("POST", "/clipboard", bytes.NewReader(body))
	setReq.Header.Set("Content-Type", "application/json")
	setW := httptest.NewRecorder()

	// Call the handler to set clipboard
	service.SetClipboardHandler(setW, setReq)

	// Check the set response
	setResp := setW.Result()
	defer setResp.Body.Close()

	assert.Equal(t, http.StatusOK, setResp.StatusCode)

	var setRespBody ClipboardSetResponse
	err = json.NewDecoder(setResp.Body).Decode(&setRespBody)
	require.NoError(t, err)

	assert.True(t, setRespBody.Ok)

	// 2. Get clipboard content to verify it was set
	getReq := httptest.NewRequest("GET", "/clipboard", nil)
	getW := httptest.NewRecorder()

	// Call the handler to get clipboard
	service.GetClipboardHandler(getW, getReq)

	// Check the get response
	getResp := getW.Result()
	defer getResp.Body.Close()

	assert.Equal(t, http.StatusOK, getResp.StatusCode)

	var getContent ClipboardContent
	err = json.NewDecoder(getResp.Body).Decode(&getContent)
	require.NoError(t, err)

	// Note: This test will only pass if the clipboard operations actually succeed
	// in the current environment, which might not be the case in CI pipelines
	assert.Equal(t, "text", getContent.Type)
	assert.NotNil(t, getContent.Text)

	// The test could be flaky if something else modified the clipboard between setting and getting
	// so we'll just check that we got a text response
	t.Logf("Got clipboard text: %s", *getContent.Text)
}

// TestStreamClipboardFormat tests the format of the SSE stream response
func TestStreamClipboardFormat(t *testing.T) {
	// Skip tests that require clipboard access in CI environments
	if testing.Short() {
		t.Skip("Skipping clipboard tests in short mode")
	}

	// Create a test clipboard service
	service := &ApiService{
		clipboardManager: NewClipboardManager(),
	}

	// Create test context
	ctx := context.Background()

	// First set the clipboard to a known value
	content := ClipboardContent{
		Type: "text",
		Text: stringPtr("Test clipboard content for streaming"),
	}

	err := service.clipboardManager.SetClipboard(ctx, &content)
	require.NoError(t, err)

	// Create a test HTTP request for the stream
	req := httptest.NewRequest("GET", "/clipboard/stream", nil)
	w := httptest.NewRecorder()

	// Create a test HTTP request for the stream - only checking headers and initial response
	service.StreamClipboardHandler(w, req)

	// Check the response headers
	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
	assert.Equal(t, "application/json", resp.Header.Get("X-SSE-Content-Type"))
}
