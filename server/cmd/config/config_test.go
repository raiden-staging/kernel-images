package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	testCases := []struct {
		name    string
		env     map[string]string
		wantErr bool
		wantCfg *Config
	}{
		{
			name: "defaults (no env set)",
			env:  map[string]string{},
			wantCfg: &Config{
				Port:                    10001,
				FrameRate:               10,
				DisplayNum:              1,
				MaxSizeInMB:             500,
				OutputDir:               ".",
				PathToFFmpeg:            "ffmpeg",
				RTMPListenAddr:          ":1935",
				RTMPSListenAddr:         ":1936",
				RTMPSCertPath:           "",
				RTMPSKeyPath:            "",
				VirtualVideoDevice:      "/dev/video20",
				VirtualAudioSink:        "audio_input",
				VirtualMicrophoneSource: "microphone",
				VirtualInputWidth:       1280,
				VirtualInputHeight:      720,
				VirtualInputFrameRate:   30,
			},
		},
		{
			name: "custom valid env",
			env: map[string]string{
				"PORT":              "12345",
				"FRAME_RATE":        "20",
				"DISPLAY_NUM":       "2",
				"MAX_SIZE_MB":       "250",
				"OUTPUT_DIR":        "/tmp",
				"FFMPEG_PATH":       "/usr/local/bin/ffmpeg",
				"RTMP_LISTEN_ADDR":  "0.0.0.0:1935",
				"RTMPS_LISTEN_ADDR": "0.0.0.0:1936",
				"RTMPS_CERT_PATH":   "/cert.pem",
				"RTMPS_KEY_PATH":    "/key.pem",
			},
			wantCfg: &Config{
				Port:                    12345,
				FrameRate:               20,
				DisplayNum:              2,
				MaxSizeInMB:             250,
				OutputDir:               "/tmp",
				PathToFFmpeg:            "/usr/local/bin/ffmpeg",
				RTMPListenAddr:          "0.0.0.0:1935",
				RTMPSListenAddr:         "0.0.0.0:1936",
				RTMPSCertPath:           "/cert.pem",
				RTMPSKeyPath:            "/key.pem",
				VirtualVideoDevice:      "/dev/video20",
				VirtualAudioSink:        "audio_input",
				VirtualMicrophoneSource: "microphone",
				VirtualInputWidth:       1280,
				VirtualInputHeight:      720,
				VirtualInputFrameRate:   30,
			},
		},
		{
			name: "custom virtual input env",
			env: map[string]string{
				"VIRTUAL_INPUT_VIDEO_DEVICE":      "/dev/video42",
				"VIRTUAL_INPUT_AUDIO_SINK":        "custom_sink",
				"VIRTUAL_INPUT_MICROPHONE_SOURCE": "custom_mic",
				"VIRTUAL_INPUT_WIDTH":             "800",
				"VIRTUAL_INPUT_HEIGHT":            "600",
				"VIRTUAL_INPUT_FRAME_RATE":        "25",
			},
			wantCfg: &Config{
				Port:                    10001,
				FrameRate:               10,
				DisplayNum:              1,
				MaxSizeInMB:             500,
				OutputDir:               ".",
				PathToFFmpeg:            "ffmpeg",
				RTMPListenAddr:          ":1935",
				RTMPSListenAddr:         ":1936",
				RTMPSCertPath:           "",
				RTMPSKeyPath:            "",
				VirtualVideoDevice:      "/dev/video42",
				VirtualAudioSink:        "custom_sink",
				VirtualMicrophoneSource: "custom_mic",
				VirtualInputWidth:       800,
				VirtualInputHeight:      600,
				VirtualInputFrameRate:   25,
			},
		},
		{
			name: "negative display num",
			env: map[string]string{
				"DISPLAY_NUM": "-1",
			},
			wantErr: true,
		},
		{
			name: "frame rate too high",
			env: map[string]string{
				"FRAME_RATE": "1201",
			},
			wantErr: true,
		},
		{
			name: "max size too big",
			env: map[string]string{
				"MAX_SIZE_MB": "10001",
			},
			wantErr: true,
		},
		{
			name: "missing ffmpeg path (set to empty)",
			env: map[string]string{
				"FFMPEG_PATH": "",
			},
			wantErr: true,
		},
		{
			name: "missing output dir (set to empty)",
			env: map[string]string{
				"OUTPUT_DIR": "",
			},
			wantErr: true,
		},
		{
			name: "rtmp listen required",
			env: map[string]string{
				"RTMP_LISTEN_ADDR": "",
			},
			wantErr: true,
		},
		{
			name: "rtmps cert and key must both be set",
			env: map[string]string{
				"RTMPS_CERT_PATH": "/cert",
			},
			wantErr: true,
		},
	}

	for idx := range testCases {
		tc := testCases[idx]
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			cfg, err := Load()

			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, cfg)
				require.Equal(t, tc.wantCfg, cfg)
			}
		})
	}
}
