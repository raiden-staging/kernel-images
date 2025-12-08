package virtualinputs

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

const (
	pipeOpenRetryInterval  = 150 * time.Millisecond
	DefaultPipeOpenTimeout = 2 * time.Second
)

// OpenPipeReadWriter opens a FIFO in read/write mode to keep both ends alive without blocking.
func OpenPipeReadWriter(path string, timeout time.Duration) (*os.File, error) {
	if path == "" {
		return nil, fmt.Errorf("pipe path required")
	}
	if timeout <= 0 {
		timeout = DefaultPipeOpenTimeout
	}
	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
		if err == nil {
			return f, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("open pipe %s: %w", path, err)
			}
			time.Sleep(pipeOpenRetryInterval)
			continue
		}
		return nil, fmt.Errorf("open pipe %s: %w", path, err)
	}
}

// OpenPipeWriter opens a FIFO for writing without blocking indefinitely when no reader is present.
// It retries until a reader appears or the timeout elapses.
func OpenPipeWriter(path string, timeout time.Duration) (*os.File, error) {
	if path == "" {
		return nil, fmt.Errorf("pipe path required")
	}
	if timeout <= 0 {
		timeout = DefaultPipeOpenTimeout
	}
	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			if err := syscall.SetNonblock(int(f.Fd()), false); err != nil {
				_ = f.Close()
				return nil, fmt.Errorf("open pipe %s: %w", path, err)
			}
			return f, nil
		}
		if errors.Is(err, syscall.ENXIO) || errors.Is(err, os.ErrNotExist) {
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("open pipe %s: %w", path, err)
			}
			time.Sleep(pipeOpenRetryInterval)
			continue
		}
		return nil, fmt.Errorf("open pipe %s: %w", path, err)
	}
}
