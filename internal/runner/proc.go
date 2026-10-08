// Package runner supervises analyzer processes and schedules check tasks within resource budgets.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

const DefaultMaxOutput = 64 << 20

type Cmd struct {
	Path string
	Args []string
	Dir  string
	// Env is the complete environment; the parent environment is never inherited implicitly.
	Env       []string
	Stdin     io.Reader
	MaxOutput int
	Timeout   time.Duration
	// Cleanup releases resources outside the process tree (e.g. a container) after cancellation.
	Cleanup func()
}

type Result struct {
	ExitCode  int
	Stdout    []byte
	Stderr    []byte
	Truncated bool
	TimedOut  bool
	Cancelled bool
	Duration  time.Duration
}

// Run executes c with its own process group so cancellation and completion reap every descendant.
func Run(ctx context.Context, c Cmd) (Result, error) {
	if c.MaxOutput == 0 {
		c.MaxOutput = DefaultMaxOutput
	}
	runCtx := ctx
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}

	cmd := exec.Command(c.Path, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	cmd.Stdin = c.Stdin
	stdout := &capWriter{max: c.MaxOutput}
	stderr := &capWriter{max: c.MaxOutput}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	setProcessGroup(cmd)
	// Without this, a lingering descendant that inherited the pipes would block Wait forever.
	cmd.WaitDelay = 2 * time.Second

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{ExitCode: -1}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var waitErr error
	res := Result{}
	select {
	case waitErr = <-done:
	case <-runCtx.Done():
		killTree(cmd)
		waitErr = <-done
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			res.TimedOut = true
		} else {
			res.Cancelled = true
		}
	}
	// Descendants that outlive the tool would keep consuming resources and could hold files open.
	reapGroup(cmd)
	if (res.TimedOut || res.Cancelled) && c.Cleanup != nil {
		c.Cleanup()
	}

	res.Duration = time.Since(start)
	res.Stdout, res.Stderr = stdout.Bytes(), stderr.Bytes()
	res.Truncated = stdout.overflow || stderr.overflow
	res.ExitCode = cmd.ProcessState.ExitCode()
	if res.TimedOut || res.Cancelled {
		return res, fmt.Errorf("%s: %w", c.Path, runCtx.Err())
	}
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) && !errors.Is(waitErr, exec.ErrWaitDelay) {
		return res, waitErr
	}
	return res, nil
}

// capWriter keeps the first max bytes and records overflow instead of growing without bound.
type capWriter struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	room := w.max - w.buf.Len()
	if room <= 0 {
		w.overflow = w.overflow || len(p) > 0
		return len(p), nil
	}
	if len(p) > room {
		w.buf.Write(p[:room])
		w.overflow = true
		return len(p), nil
	}
	w.buf.Write(p)
	return len(p), nil
}

func (w *capWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Clone(w.buf.Bytes())
}

// Proc is a long-lived supervised process with stdio pipes, used for workers and language servers.
type Proc struct {
	cmd     *exec.Cmd
	Stdin   io.WriteCloser
	Stdout  io.ReadCloser
	stderr  *capWriter
	done    chan struct{}
	err     error
	cleanup func()
}

func Start(c Cmd) (*Proc, error) {
	if c.MaxOutput == 0 {
		c.MaxOutput = 1 << 20
	}
	cmd := exec.Command(c.Path, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	p := &Proc{cmd: cmd, stderr: &capWriter{max: c.MaxOutput}, done: make(chan struct{}), cleanup: c.Cleanup}
	cmd.Stderr = p.stderr
	setProcessGroup(cmd)
	var err error
	if p.Stdin, err = cmd.StdinPipe(); err != nil {
		return nil, err
	}
	// An os.Pipe owned by us, unlike StdoutPipe, is not closed by Wait, so buffered output is never lost.
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = w
	if err := cmd.Start(); err != nil {
		r.Close()
		w.Close()
		return nil, err
	}
	w.Close()
	p.Stdout = r
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

func (p *Proc) Pid() int { return p.cmd.Process.Pid }

// Done is closed once the process has exited and been reaped.
func (p *Proc) Done() <-chan struct{} { return p.done }

func (p *Proc) Stderr() []byte { return p.stderr.Bytes() }

// Kill terminates the process tree and waits for the leader to be reaped.
func (p *Proc) Kill() {
	killTree(p.cmd)
	<-p.done
	if p.cleanup != nil {
		p.cleanup()
	}
}

// Wait waits up to grace for a voluntary exit, then kills the tree.
func (p *Proc) Wait(grace time.Duration) error {
	select {
	case <-p.done:
		reapGroup(p.cmd)
	case <-time.After(grace):
		p.Kill()
	}
	return p.err
}
