package api

import (
	"context"
	"testing"

	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
	"github.com/stretchr/testify/assert"
)

func TestClipboardContentTypes(t *testing.T) {
	// Simple test to verify the clipboard content types are defined correctly
	assert.Equal(t, oapi.ClipboardContentType("text"), oapi.Text)
	assert.Equal(t, oapi.ClipboardContentType("image"), oapi.Image)
}

func TestClipboardResponseTypes(t *testing.T) {
	// Test clipboard content response creation
	text := "Test clipboard content"
	clipboardContent := oapi.ClipboardContent{
		Type: oapi.Text,
		Text: &text,
	}

	// Test GetClipboard200JSONResponse
	getResponse := oapi.GetClipboard200JSONResponse(clipboardContent)
	assert.Equal(t, oapi.Text, getResponse.Type)
	assert.Equal(t, &text, getResponse.Text)

	// Test SetClipboard200JSONResponse
	setResponse := oapi.SetClipboard200JSONResponse{
		Ok: true,
	}
	assert.True(t, setResponse.Ok)

	// Test SetClipboard400JSONResponse
	errorMsg := "error message"
	badRequestResponse := oapi.SetClipboard400JSONResponse{
		BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{
			Message: errorMsg,
		},
	}
	assert.Equal(t, errorMsg, badRequestResponse.Message)
}

// Additional test for clipboard functionality would be added here in a production environment
// These would include integration tests with actual clipboard operations
// For this implementation, we'll rely on manual testing since the actual clipboard
// functionality requires a running X server with DISPLAY environment variable set correctly

func (m *mockClipboardManager) getClipboardText(ctx context.Context) (string, error) {
	if m.getTextFunc != nil {
		return m.getTextFunc(ctx)
	}
	return "", nil
}

func (m *mockClipboardManager) setClipboardText(ctx context.Context, text string) error {
	if m.setTextFunc != nil {
		return m.setTextFunc(ctx, text)
	}
	return nil
}

func setupApiServiceWithMockClipboard(t *testing.T) (*ApiService, *mockClipboardManager) {
	t.Helper()

	mockRecordManager := recorder.NewInMemoryRecordManager()
	mockFactory := func(id string, params recorder.FFmpegRecordingParams) (recorder.Recorder, error) {
		return recorder.NewMockRecorder(id), nil
	}

	service, err := New(mockRecordManager, mockFactory)
	require.NoError(t, err)

	// Initialize with mock clipboard manager
	mockCM := &mockClipboardManager{}
	service.clipboardManager = &clipboardManager{
		watchers:      make(map[string]chan oapi.ClipboardEvent),
		lastEventTime: time.Now(),
	}

	// Override the getClipboardText and setClipboardText methods
	origGetText := service.clipboardManager.getClipboardText
	origSetText := service.clipboardManager.setClipboardText

	service.clipboardManager.getClipboardText = func(ctx context.Context) (string, error) {
		return mockCM.getClipboardText(ctx)
	}

	service.clipboardManager.setClipboardText = func(ctx context.Context, text string) error {
		return mockCM.setClipboardText(ctx, text)
	}

	// Cleanup function to restore original methods
	t.Cleanup(func() {
		service.clipboardManager.getClipboardText = origGetText
		service.clipboardManager.setClipboardText = origSetText
	})

	return service, mockCM
}

func TestGetClipboard(t *testing.T) {
	ctx := logger.WithLogger(context.Background())

	t.Run("returns text from clipboard", func(t *testing.T) {
		service, mockCM := setupApiServiceWithMockClipboard(t)
		expectedText := "Test clipboard content"

		mockCM.getTextFunc = func(ctx context.Context) (string, error) {
			return expectedText, nil
		}

		response, err := service.GetClipboard(ctx, oapi.GetClipboardRequestObject{})
		require.NoError(t, err)

		getResponse, ok := response.(oapi.GetClipboard200JSONResponse)
		require.True(t, ok, "Expected GetClipboard200JSONResponse")
		assert.Equal(t, oapi.Text, getResponse.Type)
		require.NotNil(t, getResponse.Text)
		assert.Equal(t, expectedText, *getResponse.Text)
	})
}

func TestSetClipboard(t *testing.T) {
	ctx := logger.WithLogger(context.Background())

	t.Run("sets text to clipboard", func(t *testing.T) {
		service, mockCM := setupApiServiceWithMockClipboard(t)
		textToSet := "New clipboard content"
		var textSet string

		mockCM.setTextFunc = func(ctx context.Context, text string) error {
			textSet = text
			return nil
		}

		request := oapi.SetClipboardRequestObject{
			Body: oapi.ClipboardContent{
				Type: oapi.Text,
				Text: &textToSet,
			},
		}

		response, err := service.SetClipboard(ctx, request)
		require.NoError(t, err)

		setResponse, ok := response.(oapi.SetClipboard200JSONResponse)
		require.True(t, ok, "Expected SetClipboard200JSONResponse")
		assert.True(t, setResponse.Ok)
		assert.Equal(t, textToSet, textSet)
	})

	t.Run("validates content type", func(t *testing.T) {
		service, _ := setupApiServiceWithMockClipboard(t)
		imageType := oapi.Image

		request := oapi.SetClipboardRequestObject{
			Body: oapi.ClipboardContent{
				Type: imageType,
			},
		}

		response, err := service.SetClipboard(ctx, request)
		require.NoError(t, err)

		badRequestResponse, ok := response.(oapi.SetClipboard400JSONResponse)
		require.True(t, ok, "Expected SetClipboard400JSONResponse")
		assert.Contains(t, badRequestResponse.Message, "only text clipboard content is supported")
	})

	t.Run("validates text content presence", func(t *testing.T) {
		service, _ := setupApiServiceWithMockClipboard(t)

		request := oapi.SetClipboardRequestObject{
			Body: oapi.ClipboardContent{
				Type: oapi.Text,
			},
		}

		response, err := service.SetClipboard(ctx, request)
		require.NoError(t, err)

		badRequestResponse, ok := response.(oapi.SetClipboard400JSONResponse)
		require.True(t, ok, "Expected SetClipboard400JSONResponse")
		assert.Contains(t, badRequestResponse.Message, "text content is required")
	})
}

func TestStreamClipboard(t *testing.T) {
	ctx, cancel := context.WithCancel(logger.WithLogger(context.Background()))
	defer cancel()

	t.Run("starts stream and sends events", func(t *testing.T) {
		service, mockCM := setupApiServiceWithMockClipboard(t)

		// Mock clipboard content changes
		textContents := []string{"Initial content", "Updated content"}
		currentIndex := 0

		mockCM.getTextFunc = func(ctx context.Context) (string, error) {
			return textContents[currentIndex], nil
		}

		// Set up a goroutine to simulate clipboard changes
		go func() {
			time.Sleep(100 * time.Millisecond)
			currentIndex = 1
			time.Sleep(100 * time.Millisecond)
			cancel() // Cancel context to end test
		}()

		response, err := service.StreamClipboard(ctx, oapi.StreamClipboardRequestObject{})
		require.NoError(t, err)

		streamResponse, ok := response.(*oapi.StreamClipboardResponseStream)
		require.True(t, ok, "Expected StreamClipboardResponseStream")

		// Verify the stream is properly set up
		assert.Equal(t, "application/json", streamResponse.Headers.XSSEContentType)
		assert.NotNil(t, streamResponse.Events)
		assert.NotNil(t, streamResponse.Cleanup)

		// Read at most 2 events (with timeout)
		receivedEvents := make([]oapi.ClipboardEvent, 0, 2)
		timeout := time.After(500 * time.Millisecond)

	LOOP:
		for i := 0; i < 2; i++ {
			select {
			case event := <-streamResponse.Events:
				receivedEvents = append(receivedEvents, event)
			case <-timeout:
				break LOOP
			}
		}

		// Clean up
		streamResponse.Cleanup()

		// We may get only the second event or both, depending on timing
		assert.GreaterOrEqual(t, len(receivedEvents), 1, "Should have received at least one event")

		// Verify event properties
		for _, event := range receivedEvents {
			assert.Equal(t, oapi.Text, event.Type)
			assert.NotNil(t, event.Preview)
			assert.NotEmpty(t, event.Ts)
		}
	})
}
