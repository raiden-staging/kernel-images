package virtualinputs

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenPipeWriterSucceedsWithReader(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pipe := filepath.Join(dir, "video.pipe")
	require.NoError(t, syscall.Mkfifo(pipe, 0o666))

	reader, err := os.OpenFile(pipe, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	require.NoError(t, err)
	defer reader.Close()

	writer, err := OpenPipeWriter(pipe, time.Second)
	require.NoError(t, err)
	require.NotNil(t, writer)
	require.NoError(t, writer.Close())
}

func TestOpenPipeWriterTimesOutWithoutReader(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pipe := filepath.Join(dir, "audio.pipe")
	require.NoError(t, syscall.Mkfifo(pipe, 0o666))

	start := time.Now()
	_, err := OpenPipeWriter(pipe, 200*time.Millisecond)
	require.Error(t, err)
	require.GreaterOrEqual(t, time.Since(start), 180*time.Millisecond)
}

func TestOpenPipeReadWriterDoesNotBlockWithoutPeer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pipe := filepath.Join(dir, "keepalive.pipe")
	require.NoError(t, syscall.Mkfifo(pipe, 0o666))

	f, err := OpenPipeReadWriter(pipe, time.Second)
	require.NoError(t, err)
	require.NotNil(t, f)
	require.NoError(t, f.Close())
}
