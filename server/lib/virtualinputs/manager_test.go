package virtualinputs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeSourceDefaults(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		src     *MediaSource
		isVideo bool
		wantURL string
		wantFmt string
	}{
		{
			name:    "socket video uses default pipe and mpegts",
			src:     &MediaSource{Type: SourceTypeSocket},
			isVideo: true,
			wantURL: defaultVideoPipe,
			wantFmt: "mpegts",
		},
		{
			name:    "socket audio uses default pipe and mp3",
			src:     &MediaSource{Type: SourceTypeSocket},
			isVideo: false,
			wantURL: defaultAudioPipe,
			wantFmt: "mp3",
		},
		{
			name:    "webrtc video uses default pipe and ivf",
			src:     &MediaSource{Type: SourceTypeWebRTC},
			isVideo: true,
			wantURL: defaultVideoPipe,
			wantFmt: "ivf",
		},
		{
			name:    "webrtc audio uses default pipe and ogg",
			src:     &MediaSource{Type: SourceTypeWebRTC},
			isVideo: false,
			wantURL: defaultAudioPipe,
			wantFmt: "ogg",
		},
	}

	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeSource(tt.src, tt.isVideo)
			require.NotNil(t, got)
			require.Equal(t, tt.wantURL, got.URL)
			require.Equal(t, tt.wantFmt, got.Format)
		})
	}
}

func TestBuildIngestStatus(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Video: &MediaSource{Type: SourceTypeSocket, URL: "/tmp/vid.pipe", Format: "mpegts"},
		Audio: &MediaSource{Type: SourceTypeWebRTC, URL: "/tmp/aud.pipe", Format: "ogg"},
	}
	status := buildIngestStatus(cfg)
	require.NotNil(t, status)
	require.NotNil(t, status.Video)
	require.NotNil(t, status.Audio)
	require.Equal(t, string(SourceTypeSocket), status.Video.Protocol)
	require.Equal(t, "mpegts", status.Video.Format)
	require.Equal(t, "/tmp/vid.pipe", status.Video.Path)
	require.Equal(t, string(SourceTypeWebRTC), status.Audio.Protocol)
	require.Equal(t, "ogg", status.Audio.Format)
	require.Equal(t, "/tmp/aud.pipe", status.Audio.Path)

	require.Nil(t, buildIngestStatus(Config{}))
}

func TestBuildInputArgsIncludesFormatForRealtimeSources(t *testing.T) {
	t.Parallel()

	videoArgs := buildInputArgs(&MediaSource{Type: SourceTypeSocket, URL: "/tmp/video.pipe", Format: "mpegts"})
	require.Equal(t, []string{"-thread_queue_size", "64", "-f", "mpegts", "-i", "/tmp/video.pipe"}, videoArgs)

	audioArgs := buildInputArgs(&MediaSource{Type: SourceTypeWebRTC, URL: "/tmp/audio.pipe", Format: "ogg"})
	require.Equal(t, []string{"-thread_queue_size", "64", "-f", "ogg", "-i", "/tmp/audio.pipe"}, audioArgs)
}

func TestNormalizeConfigAcceptsRealtimeSourcesWithoutURLs(t *testing.T) {
	t.Parallel()

	mgr := NewManager("", "", "", "", 0, 0, 0, nil)
	cfg, err := mgr.normalizeConfig(Config{
		Video: &MediaSource{Type: SourceTypeSocket},
		Audio: &MediaSource{Type: SourceTypeWebRTC},
	})
	require.NoError(t, err)
	require.Equal(t, SourceTypeSocket, cfg.Video.Type)
	require.Equal(t, defaultVideoPipe, cfg.Video.URL)
	require.Equal(t, "mpegts", cfg.Video.Format)
	require.Equal(t, SourceTypeWebRTC, cfg.Audio.Type)
	require.Equal(t, defaultAudioPipe, cfg.Audio.URL)
	require.Equal(t, "ogg", cfg.Audio.Format)
}

func TestNormalizeConfigValidatesTypesAndNormalizes(t *testing.T) {
	t.Parallel()

	mgr := NewManager("", "", "", "", 0, 0, 0, nil)

	_, err := mgr.normalizeConfig(Config{Video: &MediaSource{}})
	require.ErrorIs(t, err, ErrVideoTypeRequired)

	_, err = mgr.normalizeConfig(Config{Audio: &MediaSource{}})
	require.ErrorIs(t, err, ErrAudioTypeRequired)

	cfg, err := mgr.normalizeConfig(Config{
		Video: &MediaSource{Type: "WebRTC"},
		Audio: &MediaSource{Type: "SoCkEt"},
	})
	require.NoError(t, err)
	require.Equal(t, SourceTypeWebRTC, cfg.Video.Type)
	require.Equal(t, SourceTypeSocket, cfg.Audio.Type)
}
