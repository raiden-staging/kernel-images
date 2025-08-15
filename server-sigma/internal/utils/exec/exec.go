package execx

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"time"
)

type Result struct {
	Code     int
	Stdout   []byte
	Stderr   []byte
	Duration time.Duration
}

func ExecCapture(ctx context.Context, bin string, args []string, env []string, cwd string) (*Result, error) {
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
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			return &Result{Code: -1, Stdout: outBuf.Bytes(), Stderr: errBuf.Bytes(), Duration: dur}, err
		}
	}
	return &Result{Code: code, Stdout: outBuf.Bytes(), Stderr: errBuf.Bytes(), Duration: dur}, nil
}

type Spawned struct {
	Cmd       *exec.Cmd
	StartedAt time.Time
}

func ExecSpawn(ctx context.Context, bin string, args []string, env []string, cwd string, stdout, stderr *bytes.Buffer) (*Spawned, error) {
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
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Spawned{Cmd: cmd, StartedAt: time.Now()}, nil
}