package config

import (
	"fmt"

	"github.com/kelseyhightower/envconfig"
)

// Config holds all configuration for the server
type Config struct {
	// Server configuration
	Port int `envconfig:"PORT" default:"10001"`

	// Recording configuration
	FrameRate   int    `envconfig:"FRAME_RATE" default:"10"`
	DisplayNum  int    `envconfig:"DISPLAY_NUM" default:"1"`
	MaxSizeInMB int    `envconfig:"MAX_SIZE_MB" default:"500"`
	OutputDir   string `envconfig:"OUTPUT_DIR" default:"."`

	// Absolute or relative path to the ffmpeg binary. If empty the code falls back to "ffmpeg" on $PATH.
	PathToFFmpeg string `envconfig:"FFMPEG_PATH" default:"ffmpeg"`

	// RTMP/RTMPS internal server configuration
	RTMPListenAddr  string `envconfig:"RTMP_LISTEN_ADDR" default:":1935"`
	RTMPSListenAddr string `envconfig:"RTMPS_LISTEN_ADDR" default:":1936"`
	RTMPSCertPath   string `envconfig:"RTMPS_CERT_PATH" default:""`
	RTMPSKeyPath    string `envconfig:"RTMPS_KEY_PATH" default:""`

	// Virtual input defaults
	VirtualVideoDevice      string `envconfig:"VIRTUAL_INPUT_VIDEO_DEVICE" default:"/dev/video20"`
	VirtualAudioSink        string `envconfig:"VIRTUAL_INPUT_AUDIO_SINK" default:"audio_input"`
	VirtualMicrophoneSource string `envconfig:"VIRTUAL_INPUT_MICROPHONE_SOURCE" default:"microphone"`
	VirtualInputWidth       int    `envconfig:"VIRTUAL_INPUT_WIDTH" default:"1280"`
	VirtualInputHeight      int    `envconfig:"VIRTUAL_INPUT_HEIGHT" default:"720"`
	VirtualInputFrameRate   int    `envconfig:"VIRTUAL_INPUT_FRAME_RATE" default:"30"`

	// DevTools proxy configuration
	LogCDPMessages bool `envconfig:"LOG_CDP_MESSAGES" default:"false"`
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	var config Config
	if err := envconfig.Process("", &config); err != nil {
		return nil, err
	}
	if err := validate(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

func validate(config *Config) error {
	if config.OutputDir == "" {
		return fmt.Errorf("OUTPUT_DIR is required")
	}
	if config.DisplayNum < 0 {
		return fmt.Errorf("DISPLAY_NUM must be greater than 0")
	}
	if config.FrameRate < 0 || config.FrameRate > 20 {
		return fmt.Errorf("FRAME_RATE must be greater than 0 and less than or equal to 20")
	}
	if config.MaxSizeInMB < 0 || config.MaxSizeInMB > 1000 {
		return fmt.Errorf("MAX_SIZE_MB must be greater than 0 and less than or equal to 1000")
	}
	if config.PathToFFmpeg == "" {
		return fmt.Errorf("FFMPEG_PATH is required")
	}
	if config.RTMPListenAddr == "" {
		return fmt.Errorf("RTMP_LISTEN_ADDR is required")
	}
	if (config.RTMPSCertPath == "") != (config.RTMPSKeyPath == "") {
		return fmt.Errorf("RTMPS_CERT_PATH and RTMPS_KEY_PATH must both be set or both be empty")
	}
	if config.VirtualVideoDevice == "" {
		return fmt.Errorf("VIRTUAL_INPUT_VIDEO_DEVICE is required")
	}
	if config.VirtualAudioSink == "" {
		return fmt.Errorf("VIRTUAL_INPUT_AUDIO_SINK is required")
	}
	if config.VirtualMicrophoneSource == "" {
		return fmt.Errorf("VIRTUAL_INPUT_MICROPHONE_SOURCE is required")
	}
	if config.VirtualInputWidth <= 0 || config.VirtualInputHeight <= 0 {
		return fmt.Errorf("VIRTUAL_INPUT_WIDTH and VIRTUAL_INPUT_HEIGHT must be greater than 0")
	}
	if config.VirtualInputFrameRate <= 0 || config.VirtualInputFrameRate > 60 {
		return fmt.Errorf("VIRTUAL_INPUT_FRAME_RATE must be between 1 and 60")
	}

	return nil
}
