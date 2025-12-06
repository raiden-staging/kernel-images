package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"testing"
	"time"

	"log/slog"

	"github.com/onkernel/kernel-images/server/lib/devtoolsproxy"
	"github.com/onkernel/kernel-images/server/lib/nekoclient"
	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
	"github.com/onkernel/kernel-images/server/lib/recorder"
	"github.com/onkernel/kernel-images/server/lib/scaletozero"
	"github.com/onkernel/kernel-images/server/lib/stream"
	"github.com/onkernel/kernel-images/server/lib/virtualinputs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestApiService(t *testing.T, mgr recorder.RecordManager) (*ApiService, *mockVirtualInputsManager) {
	vimgr := newMockVirtualInputsManager()
	return newApiServiceWithVirtualInputs(t, mgr, vimgr), vimgr
}

func newApiServiceWithVirtualInputs(t *testing.T, mgr recorder.RecordManager, vimgr *mockVirtualInputsManager) *ApiService {
	t.Helper()
	if vimgr == nil {
		vimgr = newMockVirtualInputsManager()
	}
	defaults := testStreamDefaults()
	svc, err := New(
		mgr,
		newMockFactory(),
		newTestUpstreamManager(),
		scaletozero.NewNoopController(),
		newMockNekoClient(t),
		vimgr,
		stream.NewStreamManager(),
		newMockStreamFactory(),
		mockRTMPServer{},
		defaults,
		"ffmpeg",
	)
	require.NoError(t, err)
	return svc
}

func TestApiService_StartRecording(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mgr := recorder.NewFFmpegManager()
		svc, _ := newTestApiService(t, mgr)

		resp, err := svc.StartRecording(ctx, oapi.StartRecordingRequestObject{})
		require.NoError(t, err)
		require.IsType(t, oapi.StartRecording201Response{}, resp)

		rec, exists := mgr.GetRecorder("default")
		require.True(t, exists, "recorder was not registered")
		require.True(t, rec.IsRecording(ctx), "recorder should be recording after Start")
	})

	t.Run("already recording", func(t *testing.T) {
		mgr := recorder.NewFFmpegManager()
		svc, _ := newTestApiService(t, mgr)

		// First start should succeed
		_, err := svc.StartRecording(ctx, oapi.StartRecordingRequestObject{})
		require.NoError(t, err)

		// Second start should return conflict
		resp, err := svc.StartRecording(ctx, oapi.StartRecordingRequestObject{})
		require.NoError(t, err)
		require.IsType(t, oapi.StartRecording409JSONResponse{}, resp)
	})

	t.Run("custom ids don't collide", func(t *testing.T) {
		mgr := recorder.NewFFmpegManager()
		svc, _ := newTestApiService(t, mgr)

		for i := 0; i < 5; i++ {
			customID := fmt.Sprintf("rec-%d", i)
			resp, err := svc.StartRecording(ctx, oapi.StartRecordingRequestObject{Body: &oapi.StartRecordingJSONRequestBody{Id: &customID}})
			require.NoError(t, err)
			require.IsType(t, oapi.StartRecording201Response{}, resp)

			rec, exists := mgr.GetRecorder(customID)
			assert.True(t, exists)
			assert.True(t, rec.IsRecording(ctx))
		}

		out := mgr.ListActiveRecorders(ctx)
		assert.Equal(t, 5, len(out))
		for _, rec := range out {
			assert.NotEqual(t, "default", rec.ID())
		}

		require.NoError(t, mgr.StopAll(ctx))

		out = mgr.ListActiveRecorders(ctx)
		assert.Equal(t, 5, len(out))
	})
}

func TestApiService_StopRecording(t *testing.T) {
	ctx := context.Background()

	t.Run("no active recording", func(t *testing.T) {
		mgr := recorder.NewFFmpegManager()
		svc, _ := newTestApiService(t, mgr)

		resp, err := svc.StopRecording(ctx, oapi.StopRecordingRequestObject{})
		require.NoError(t, err)
		require.IsType(t, oapi.StopRecording400JSONResponse{}, resp)
	})

	t.Run("graceful stop", func(t *testing.T) {
		mgr := recorder.NewFFmpegManager()
		rec := &mockRecorder{id: "default", isRecordingFlag: true}
		require.NoError(t, mgr.RegisterRecorder(ctx, rec), "failed to register recorder")

		svc, _ := newTestApiService(t, mgr)
		resp, err := svc.StopRecording(ctx, oapi.StopRecordingRequestObject{})
		require.NoError(t, err)
		require.IsType(t, oapi.StopRecording200Response{}, resp)
		require.True(t, rec.stopCalled, "Stop should have been called on recorder")
	})

	t.Run("force stop", func(t *testing.T) {
		mgr := recorder.NewFFmpegManager()
		rec := &mockRecorder{id: "default", isRecordingFlag: true}
		require.NoError(t, mgr.RegisterRecorder(ctx, rec), "failed to register recorder")

		force := true
		req := oapi.StopRecordingRequestObject{Body: &oapi.StopRecordingJSONRequestBody{ForceStop: &force}}
		svc, _ := newTestApiService(t, mgr)
		resp, err := svc.StopRecording(ctx, req)
		require.NoError(t, err)
		require.IsType(t, oapi.StopRecording200Response{}, resp)
		require.True(t, rec.forceStopCalled, "ForceStop should have been called on recorder")
	})
}

func TestApiService_DownloadRecording(t *testing.T) {
	ctx := context.Background()

	t.Run("not found", func(t *testing.T) {
		mgr := recorder.NewFFmpegManager()
		svc, _ := newTestApiService(t, mgr)
		resp, err := svc.DownloadRecording(ctx, oapi.DownloadRecordingRequestObject{})
		require.NoError(t, err)
		require.IsType(t, oapi.DownloadRecording404JSONResponse{}, resp)
	})

	randomBytes := func(n int) []byte {
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(rand.Intn(256))
		}
		return data
	}

	t.Run("still recording", func(t *testing.T) {
		mgr := recorder.NewFFmpegManager()
		rec := &mockRecorder{id: "default", isRecordingFlag: true, recordingData: randomBytes(minRecordingSizeInBytes - 1)}
		require.NoError(t, mgr.RegisterRecorder(ctx, rec), "failed to register recorder")

		svc, _ := newTestApiService(t, mgr)
		// will return a 202 when the recording is too small
		resp, err := svc.DownloadRecording(ctx, oapi.DownloadRecordingRequestObject{})
		require.NoError(t, err)
		require.IsType(t, oapi.DownloadRecording202Response{}, resp)

		// mimic writing more data to the recording
		data := randomBytes(minRecordingSizeInBytes * 2)
		rec.recordingData = data

		// now that the recording is larger than the minimum size, it should return a 200
		resp, err = svc.DownloadRecording(ctx, oapi.DownloadRecordingRequestObject{})
		require.NoError(t, err)
		require.IsType(t, oapi.DownloadRecording200Videomp4Response{}, resp)
		r, ok := resp.(oapi.DownloadRecording200Videomp4Response)
		require.True(t, ok, "expected 200 mp4 response, got %T", resp)
		buf := new(bytes.Buffer)
		_, copyErr := io.Copy(buf, r.Body)
		require.NoError(t, copyErr)
		require.Equal(t, data, buf.Bytes(), "response body mismatch")
		require.Equal(t, int64(len(data)), r.ContentLength, "content length mismatch")
	})

	t.Run("success", func(t *testing.T) {
		mgr := recorder.NewFFmpegManager()
		data := []byte("dummy video data")
		rec := &mockRecorder{id: "default", recordingData: data}
		require.NoError(t, mgr.RegisterRecorder(ctx, rec), "failed to register recorder")

		svc, _ := newTestApiService(t, mgr)
		resp, err := svc.DownloadRecording(ctx, oapi.DownloadRecordingRequestObject{})
		require.NoError(t, err)
		r, ok := resp.(oapi.DownloadRecording200Videomp4Response)
		require.True(t, ok, "expected 200 mp4 response, got %T", resp)
		buf := new(bytes.Buffer)
		_, copyErr := io.Copy(buf, r.Body)
		require.NoError(t, copyErr)
		require.Equal(t, data, buf.Bytes(), "response body mismatch")
		require.Equal(t, int64(len(data)), r.ContentLength, "content length mismatch")
	})
}

func TestApiService_StreamLifecycle(t *testing.T) {
	ctx := context.Background()
	mgr := recorder.NewFFmpegManager()
	svc := newApiServiceWithVirtualInputs(t, mgr, nil)

	mode := oapi.StartStreamRequestModeInternal
	resp, err := svc.StartStream(ctx, oapi.StartStreamRequestObject{
		Body: &oapi.StartStreamJSONRequestBody{Mode: &mode},
	})
	require.NoError(t, err)
	created, ok := resp.(oapi.StartStream201JSONResponse)
	require.True(t, ok, "expected start stream response")
	assert.Equal(t, oapi.StreamInfoModeInternal, created.Mode)
	assert.True(t, created.IsStreaming)

	streamer, exists := svc.streamManager.GetStream("default")
	require.True(t, exists)
	assert.True(t, streamer.IsStreaming(ctx))

	listResp, err := svc.ListStreams(ctx, oapi.ListStreamsRequestObject{})
	require.NoError(t, err)
	listTyped, ok := listResp.(oapi.ListStreams200JSONResponse)
	require.True(t, ok)
	require.Len(t, listTyped, 1)
	assert.Equal(t, oapi.StreamInfoModeInternal, listTyped[0].Mode)

	stopResp, err := svc.StopStream(ctx, oapi.StopStreamRequestObject{})
	require.NoError(t, err)
	require.IsType(t, oapi.StopStream200Response{}, stopResp)
}

func TestApiService_StartStream_RemoteValidation(t *testing.T) {
	ctx := context.Background()
	mgr := recorder.NewFFmpegManager()
	svc := newApiServiceWithVirtualInputs(t, mgr, nil)

	mode := oapi.StartStreamRequestModeRemote
	resp, err := svc.StartStream(ctx, oapi.StartStreamRequestObject{
		Body: &oapi.StartStreamJSONRequestBody{Mode: &mode},
	})
	require.NoError(t, err)
	require.IsType(t, oapi.StartStream400JSONResponse{}, resp)
}

func TestApiService_StopStream_NotFound(t *testing.T) {
	ctx := context.Background()
	mgr := recorder.NewFFmpegManager()
	svc := newApiServiceWithVirtualInputs(t, mgr, nil)

	resp, err := svc.StopStream(ctx, oapi.StopStreamRequestObject{})
	require.NoError(t, err)
	require.IsType(t, oapi.StopStream404JSONResponse{}, resp)
}

func TestApiService_Shutdown(t *testing.T) {
	ctx := context.Background()
	mgr := recorder.NewFFmpegManager()
	rec := &mockRecorder{id: "default", isRecordingFlag: true}
	require.NoError(t, mgr.RegisterRecorder(ctx, rec), "failed to register recorder")

	svc, vimgr := newTestApiService(t, mgr)
	streamer := &mockStreamer{id: "stream-1", started: true}
	require.NoError(t, svc.streamManager.RegisterStream(ctx, streamer))

	require.NoError(t, svc.Shutdown(ctx))
	require.True(t, rec.stopCalled, "Shutdown should have stopped active recorder")
	require.Equal(t, 1, vimgr.stopCalls)
	require.False(t, streamer.started, "Shutdown should stop active stream")
}

func TestApiService_VirtualInputs(t *testing.T) {
	ctx := context.Background()

	newSvc := func(t *testing.T, vimgr *mockVirtualInputsManager) *ApiService {
		mgr := recorder.NewFFmpegManager()
		return newApiServiceWithVirtualInputs(t, mgr, vimgr)
	}

	t.Run("configure success", func(t *testing.T) {
		vimgr := newMockVirtualInputsManager()
		svc := newSvc(t, vimgr)
		videoLoop := true
		audioLoop := false
		width := 640
		height := 480
		fr := 24
		startPaused := true
		videoURL := "https://example.com/vid.m3u8"
		audioURL := "https://example.com/audio.mp3"
		body := oapi.VirtualInputsRequest{
			Video: &oapi.VirtualInputSource{
				Type: oapi.Stream,
				Url:  &videoURL,
				Loop: &videoLoop,
			},
			Audio: &oapi.VirtualInputSource{
				Type: oapi.File,
				Url:  &audioURL,
				Loop: &audioLoop,
			},
			Width:       &width,
			Height:      &height,
			FrameRate:   &fr,
			StartPaused: &startPaused,
		}

		resp, err := svc.ConfigureVirtualInputs(ctx, oapi.ConfigureVirtualInputsRequestObject{Body: &body})
		require.NoError(t, err)
		out, ok := resp.(oapi.ConfigureVirtualInputs200JSONResponse)
		require.True(t, ok, "unexpected response type %T", resp)
		require.Equal(t, oapi.VirtualInputsStatusStatePaused, out.State)
		require.Len(t, vimgr.configureCalls, 1)
		call := vimgr.configureCalls[0]
		require.True(t, call.paused)
		require.NotNil(t, call.cfg.Video)
		require.NotNil(t, call.cfg.Audio)
		assert.Equal(t, "https://example.com/vid.m3u8", call.cfg.Video.URL)
		assert.Equal(t, "https://example.com/audio.mp3", call.cfg.Audio.URL)
		assert.Equal(t, 640, call.cfg.Width)
		assert.Equal(t, 480, call.cfg.Height)
		assert.Equal(t, 24, call.cfg.FrameRate)
	})

	t.Run("configure validation error", func(t *testing.T) {
		vimgr := newMockVirtualInputsManager()
		vimgr.configureErr = virtualinputs.ErrMissingSources
		svc := newSvc(t, vimgr)
		resp, err := svc.ConfigureVirtualInputs(ctx, oapi.ConfigureVirtualInputsRequestObject{Body: &oapi.VirtualInputsRequest{}})
		require.NoError(t, err)
		_, ok := resp.(oapi.ConfigureVirtualInputs400JSONResponse)
		require.True(t, ok, "expected 400 response")
	})

	t.Run("configure internal error", func(t *testing.T) {
		vimgr := newMockVirtualInputsManager()
		vimgr.configureErr = errors.New("boom")
		svc := newSvc(t, vimgr)
		reqBody := oapi.VirtualInputsRequest{
			Audio: &oapi.VirtualInputSource{Type: oapi.Stream, Url: ptr("http://a")},
		}
		resp, err := svc.ConfigureVirtualInputs(ctx, oapi.ConfigureVirtualInputsRequestObject{Body: &reqBody})
		require.NoError(t, err)
		_, ok := resp.(oapi.ConfigureVirtualInputs500JSONResponse)
		require.True(t, ok, "expected 500 response")
	})

	t.Run("pause and resume", func(t *testing.T) {
		vimgr := newMockVirtualInputsManager()
		svc := newSvc(t, vimgr)

		pauseResp, err := svc.PauseVirtualInputs(ctx, oapi.PauseVirtualInputsRequestObject{})
		require.NoError(t, err)
		_, ok := pauseResp.(oapi.PauseVirtualInputs200JSONResponse)
		require.True(t, ok)

		vimgr.resumeErr = virtualinputs.ErrNoConfigToResume
		resumeResp, err := svc.ResumeVirtualInputs(ctx, oapi.ResumeVirtualInputsRequestObject{})
		require.NoError(t, err)
		_, ok = resumeResp.(oapi.ResumeVirtualInputs400JSONResponse)
		require.True(t, ok)
	})

	t.Run("stop and status", func(t *testing.T) {
		vimgr := newMockVirtualInputsManager()
		vimgr.status.State = "running"
		svc := newSvc(t, vimgr)

		statusResp, err := svc.GetVirtualInputsStatus(ctx, oapi.GetVirtualInputsStatusRequestObject{})
		require.NoError(t, err)
		out, ok := statusResp.(oapi.GetVirtualInputsStatus200JSONResponse)
		require.True(t, ok)
		require.Equal(t, oapi.VirtualInputsStatusStateRunning, out.State)

		stopResp, err := svc.StopVirtualInputs(ctx, oapi.StopVirtualInputsRequestObject{})
		require.NoError(t, err)
		_, ok = stopResp.(oapi.StopVirtualInputs200JSONResponse)
		require.True(t, ok)
		require.Equal(t, 1, vimgr.stopCalls)
	})
}

// mockRecorder is a lightweight stand-in for recorder.Recorder used in unit tests. It purposefully
// keeps the behaviour minimal – just enough to satisfy ApiService logic. All public methods are
// safe for single-goroutine unit-test access.
type mockRecorder struct {
	id              string
	isRecordingFlag bool

	startCalled     bool
	stopCalled      bool
	forceStopCalled bool

	// configurable behaviours
	startErr      error
	stopErr       error
	forceStopErr  error
	recordingErr  error
	recordingData []byte
	deleted       bool
}

func (m *mockRecorder) ID() string { return m.id }

func (m *mockRecorder) Start(ctx context.Context) error {
	m.startCalled = true
	if m.startErr != nil {
		return m.startErr
	}
	m.isRecordingFlag = true
	return nil
}

func (m *mockRecorder) Stop(ctx context.Context) error {
	m.stopCalled = true
	if m.stopErr != nil {
		return m.stopErr
	}
	m.isRecordingFlag = false
	return nil
}

func (m *mockRecorder) ForceStop(ctx context.Context) error {
	m.forceStopCalled = true
	if m.forceStopErr != nil {
		return m.forceStopErr
	}
	m.isRecordingFlag = false
	return nil
}

func (m *mockRecorder) IsRecording(ctx context.Context) bool { return m.isRecordingFlag }

func (m *mockRecorder) Recording(ctx context.Context) (io.ReadCloser, *recorder.RecordingMetadata, error) {
	if m.deleted {
		return nil, nil, fmt.Errorf("deleted: %w", os.ErrNotExist)
	}
	if m.recordingErr != nil {
		return nil, nil, m.recordingErr
	}
	reader := io.NopCloser(bytes.NewReader(m.recordingData))
	meta := &recorder.RecordingMetadata{Size: int64(len(m.recordingData))}
	return reader, meta, nil
}

func (m *mockRecorder) Metadata() *recorder.RecordingMetadata {
	return &recorder.RecordingMetadata{}
}

func (m *mockRecorder) Delete(ctx context.Context) error {
	if m.isRecordingFlag {
		return fmt.Errorf("still recording")
	}
	m.deleted = true
	return nil
}

func (m *mockRecorder) IsDeleted(ctx context.Context) bool { return m.deleted }

func newMockFactory() recorder.FFmpegRecorderFactory {
	return func(id string, _ recorder.FFmpegRecordingParams) (recorder.Recorder, error) {
		rec := &mockRecorder{id: id}
		return rec, nil
	}
}

type mockStreamer struct {
	id       string
	meta     stream.Metadata
	started  bool
	startErr error
	stopErr  error
}

func (m *mockStreamer) ID() string { return m.id }

func (m *mockStreamer) Start(ctx context.Context) error {
	if m.startErr != nil {
		return m.startErr
	}
	m.started = true
	if m.meta.StartedAt.IsZero() {
		m.meta.StartedAt = time.Now()
	}
	return nil
}

func (m *mockStreamer) Stop(ctx context.Context) error {
	if m.stopErr != nil {
		return m.stopErr
	}
	m.started = false
	return nil
}

func (m *mockStreamer) IsStreaming(ctx context.Context) bool { return m.started }

func (m *mockStreamer) Metadata() stream.Metadata {
	meta := m.meta
	if meta.ID == "" {
		meta.ID = m.id
	}
	return meta
}

type mockRTMPServer struct{}

func (mockRTMPServer) Start(ctx context.Context) error { return nil }
func (mockRTMPServer) EnsureStream(path string)        {}
func (mockRTMPServer) IngestURL(path string) string {
	return "rtmp://internal/" + path
}
func (mockRTMPServer) PlaybackURLs(host string, path string) (*string, *string) {
	url := "rtmp://" + host + "/" + path
	return &url, nil
}
func (mockRTMPServer) Close(ctx context.Context) error { return nil }

func newMockStreamFactory() stream.FFmpegStreamerFactory {
	return func(id string, params stream.Params) (stream.Streamer, error) {
		return &mockStreamer{
			id: id,
			meta: stream.Metadata{
				ID:                id,
				Mode:              params.Mode,
				IngestURL:         params.IngestURL,
				PlaybackURL:       params.PlaybackURL,
				SecurePlaybackURL: params.SecurePlaybackURL,
				StartedAt:         time.Now(),
			},
		}, nil
	}
}

func testStreamDefaults() stream.Params {
	frameRate := 10
	display := 1
	return stream.Params{
		FrameRate:  &frameRate,
		DisplayNum: &display,
		Mode:       stream.ModeInternal,
	}
}

func newTestUpstreamManager() *devtoolsproxy.UpstreamManager {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return devtoolsproxy.NewUpstreamManager("", logger)
}

func newMockNekoClient(t *testing.T) *nekoclient.AuthClient {
	// Create a mock client that won't actually connect to anything
	// We use a dummy URL since tests don't need real Neko connectivity
	client, err := nekoclient.NewAuthClient("http://localhost:9999", "admin", "admin")
	require.NoError(t, err)
	return client
}

func ptr[T any](v T) *T {
	return &v
}

type mockVirtualInputsManager struct {
	status         virtualinputs.Status
	configureCalls []struct {
		cfg    virtualinputs.Config
		paused bool
	}
	configureErr error
	pauseErr     error
	resumeErr    error
	stopErr      error
	stopCalls    int
}

func newMockVirtualInputsManager() *mockVirtualInputsManager {
	return &mockVirtualInputsManager{
		status: virtualinputs.Status{
			State:            "idle",
			VideoDevice:      "/dev/video20",
			AudioSink:        "audio_input",
			MicrophoneSource: "microphone",
			Width:            1280,
			Height:           720,
			FrameRate:        30,
		},
	}
}

func (m *mockVirtualInputsManager) Configure(ctx context.Context, cfg virtualinputs.Config, startPaused bool) (virtualinputs.Status, error) {
	m.configureCalls = append(m.configureCalls, struct {
		cfg    virtualinputs.Config
		paused bool
	}{cfg: cfg, paused: startPaused})
	if m.configureErr != nil {
		return m.status, m.configureErr
	}
	if startPaused {
		m.status.State = "paused"
	} else {
		m.status.State = "running"
	}
	if cfg.Video != nil {
		m.status.Video = cfg.Video
	}
	if cfg.Audio != nil {
		m.status.Audio = cfg.Audio
	}
	if cfg.Width != 0 {
		m.status.Width = cfg.Width
	}
	if cfg.Height != 0 {
		m.status.Height = cfg.Height
	}
	if cfg.FrameRate != 0 {
		m.status.FrameRate = cfg.FrameRate
	}
	return m.status, nil
}

func (m *mockVirtualInputsManager) Pause(ctx context.Context) (virtualinputs.Status, error) {
	if m.pauseErr != nil {
		return m.status, m.pauseErr
	}
	m.status.State = "paused"
	return m.status, nil
}

func (m *mockVirtualInputsManager) Resume(ctx context.Context) (virtualinputs.Status, error) {
	if m.resumeErr != nil {
		return m.status, m.resumeErr
	}
	m.status.State = "running"
	return m.status, nil
}

func (m *mockVirtualInputsManager) Stop(ctx context.Context) (virtualinputs.Status, error) {
	m.stopCalls++
	if m.stopErr != nil {
		return m.status, m.stopErr
	}
	m.status.State = "idle"
	return m.status, nil
}

func (m *mockVirtualInputsManager) Status(ctx context.Context) virtualinputs.Status {
	return m.status
}

func TestApiService_PatchChromiumFlags(t *testing.T) {
	ctx := context.Background()
	mgr := recorder.NewFFmpegManager()
	svc, _ := newTestApiService(t, mgr)

	// Test with valid flags
	flags := []string{"--kiosk", "--start-maximized"}
	body := &oapi.PatchChromiumFlagsJSONRequestBody{
		Flags: flags,
	}

	req := oapi.PatchChromiumFlagsRequestObject{
		Body: body,
	}

	// This will fail to write to /chromium/flags in most test environments
	// but we're mainly testing that the handler accepts valid input
	resp, err := svc.PatchChromiumFlags(ctx, req)
	require.NoError(t, err)

	// We expect either success or an error about creating the directory
	// depending on the test environment
	switch resp.(type) {
	case oapi.PatchChromiumFlags200Response:
		// Success in environments where /chromium is writable
	case oapi.PatchChromiumFlags500JSONResponse:
		// Expected in most test environments where /chromium doesn't exist
	default:
		t.Fatalf("unexpected response type: %T", resp)
	}
}
