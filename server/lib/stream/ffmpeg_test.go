package stream

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPulseAudioMonitorSourceUsesDefaultSink(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("audio capture detection is only relevant on linux")
	}
	stubRunCommand(t, func(_ context.Context, name string, args ...string) ([]byte, error) {
		require.Equal(t, "pactl", name)
		require.Equal(t, []string{"info"}, args)
		return []byte("Default Sink: monitor_sink\n"), nil
	})

	mon, err := pulseAudioMonitorSource(context.Background())
	require.NoError(t, err)
	require.Equal(t, "monitor_sink.monitor", mon)
}

func TestPulseAudioMonitorSourceFallsBackToAudioOutput(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("audio capture detection is only relevant on linux")
	}
	callCount := 0
	stubRunCommand(t, func(_ context.Context, name string, args ...string) ([]byte, error) {
		callCount++
		switch fmt.Sprint(name, " ", args) {
		case "pactl [info]":
			return []byte("Server String: pulse\n"), nil
		case "pactl [list short sinks]":
			return []byte("0\taudio_output\tmodule-null-sink.c\ts16le 2ch 44100Hz\n1\tsecond_sink\tmodule-null-sink.c\ts16le 2ch 44100Hz\n"), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s %v", name, args)
		}
	})

	mon, err := pulseAudioMonitorSource(context.Background())
	require.NoError(t, err)
	require.Equal(t, "audio_output.monitor", mon)
	require.Equal(t, 2, callCount)
}

func TestPulseAudioMonitorSourceErrorsWithoutSinks(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("audio capture detection is only relevant on linux")
	}
	stubRunCommand(t, func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch fmt.Sprint(name, " ", args) {
		case "pactl [info]":
			return nil, errors.New("pactl unavailable")
		case "pactl [list short sinks]":
			return []byte(""), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s %v", name, args)
		}
	})

	_, err := pulseAudioMonitorSource(context.Background())
	require.Error(t, err)
}

func stubRunCommand(t *testing.T, fn func(ctx context.Context, name string, args ...string) ([]byte, error)) {
	t.Helper()
	original := runCommand
	runCommand = fn
	t.Cleanup(func() { runCommand = original })
}
