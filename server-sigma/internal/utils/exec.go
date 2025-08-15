package utils

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"time"
)

type ExecResult struct {
	Code     int
	Stdout   []byte
	Stderr   []byte
	Duration time.Duration
}

// ExecCapture runs a command to completion and returns code/stdout/stderr.
// If ctx is nil, context.Background() is used.
func ExecCapture(ctx context.Context, bin string, args []string, env []string, cwd string) (*ExecResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start)

	code := 0
	if err != nil {
		// Distinguish non-zero exit vs. start/exec failure
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			return &ExecResult{
				Code:     -1,
				Stdout:   outBuf.Bytes(),
				Stderr:   errBuf.Bytes(),
				Duration: dur,
			}, err
		}
	}

	return &ExecResult{
		Code:     code,
		Stdout:   outBuf.Bytes(),
		Stderr:   errBuf.Bytes(),
		Duration: dur,
	}, nil
}

type Spawned struct {
	Cmd       *exec.Cmd
	Stdout    bytes.Buffer
	Stderr    bytes.Buffer
	StartedAt time.Time
}

// ExecSpawn starts a long-running process and returns immediately.
// Stdout/Stderr are buffered in memory; attach readers if you prefer stream piping elsewhere.
func ExecSpawn(ctx context.Context, bin string, args []string, env []string, cwd string) (*Spawned, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &Spawned{
		Cmd:       cmd,
		Stdout:    outBuf,
		Stderr:    errBuf,
		StartedAt: time.Now(),
	}, nil
}