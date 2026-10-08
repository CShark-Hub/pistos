package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/runner"
)

// MaxFrame bounds one newline-delimited JSON-RPC message from a worker.
const MaxFrame = 16 << 20

var (
	ErrFrameTooLarge = errors.New("worker frame exceeds size limit")
	ErrWorkerExited  = errors.New("worker exited before replying")
)

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("worker error %d: %s", e.Code, e.Message) }

type InitializeParams struct {
	Protocol    int    `json:"protocol"`
	Pack        string `json:"pack"`
	PackVersion string `json:"pack_version"`
}

type InitializeResult struct {
	Protocol     int      `json:"protocol"`
	Pack         string   `json:"pack"`
	PackVersion  string   `json:"pack_version"`
	Capabilities []string `json:"capabilities"`
}

type AnalyzeParams struct {
	Capability string            `json:"capability"`
	Project    string            `json:"project"`
	Root       string            `json:"root"`
	Tree       string            `json:"tree"`
	Options    map[string]string `json:"options,omitempty"`
}

// AnalyzeResult must restate the scope it covered; a mismatched scope is treated as incomplete.
type AnalyzeResult struct {
	Status      string       `json:"status"`
	Tree        string       `json:"tree"`
	Project     string       `json:"project"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Limitations []string     `json:"limitations,omitempty"`
}

// Worker is a running out-of-process pack worker speaking newline-delimited JSON-RPC 2.0.
type Worker struct {
	proc   *runner.Proc
	reader *bufio.Reader
	mu     sync.Mutex
	nextID int64
	Info   InitializeResult
}

func StartWorker(ctx context.Context, cmd runner.Cmd, pack, packVersion string) (*Worker, error) {
	proc, err := runner.Start(cmd)
	if err != nil {
		return nil, err
	}
	w := &Worker{proc: proc, reader: bufio.NewReaderSize(proc.Stdout, 64<<10)}
	var info InitializeResult
	err = w.Call(ctx, "initialize", InitializeParams{Protocol: config.ProtocolVersion, Pack: pack, PackVersion: packVersion}, &info)
	if err == nil && info.Protocol != config.ProtocolVersion {
		err = fmt.Errorf("worker speaks protocol %d, pistos requires %d", info.Protocol, config.ProtocolVersion)
	}
	if err == nil && (info.Pack != pack || info.PackVersion != packVersion) {
		err = fmt.Errorf("worker identifies as %s@%s, expected %s@%s", info.Pack, info.PackVersion, pack, packVersion)
	}
	if err != nil {
		proc.Kill()
		return nil, err
	}
	w.Info = info
	return w, nil
}

// Call sends one request and waits for its response. Notifications received meanwhile are
// discarded: progress can never stand in for a final result.
func (w *Worker) Call(ctx context.Context, method string, params, result any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.nextID++
	id := w.nextID
	if err := w.send(rpcMessage{JSONRPC: "2.0", ID: &id, Method: method, Params: mustJSON(params)}); err != nil {
		return err
	}

	type reply struct {
		msg rpcMessage
		err error
	}
	ch := make(chan reply, 1)
	go func() {
		for {
			msg, err := w.read()
			if err != nil || (msg.ID != nil && *msg.ID == id && msg.Method == "") {
				ch <- reply{msg, err}
				return
			}
		}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			w.proc.Kill()
			return r.err
		}
		if r.msg.Error != nil {
			return r.msg.Error
		}
		if result == nil {
			return nil
		}
		if err := json.Unmarshal(r.msg.Result, result); err != nil {
			w.proc.Kill()
			return fmt.Errorf("worker %s result: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		_ = w.send(rpcMessage{JSONRPC: "2.0", Method: "cancel", Params: mustJSON(map[string]int64{"id": id})})
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
		}
		w.proc.Kill()
		return ctx.Err()
	}
}

func (w *Worker) Analyze(ctx context.Context, p AnalyzeParams) (*AnalyzeResult, error) {
	var res AnalyzeResult
	if err := w.Call(ctx, "analyze", p, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// Shutdown asks the worker to exit and kills it if it does not within the grace period.
func (w *Worker) Shutdown(ctx context.Context) error {
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := w.Call(sctx, "shutdown", nil, nil)
	w.proc.Stdin.Close()
	_ = w.proc.Wait(2 * time.Second)
	return err
}

func (w *Worker) Kill()          { w.proc.Kill() }
func (w *Worker) Pid() int       { return w.proc.Pid() }
func (w *Worker) Stderr() []byte { return w.proc.Stderr() }

func (w *Worker) send(m rpcMessage) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = w.proc.Stdin.Write(append(b, '\n'))
	return err
}

func (w *Worker) read() (rpcMessage, error) {
	var line []byte
	for {
		chunk, isPrefix, err := w.reader.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return rpcMessage{}, ErrWorkerExited
			}
			return rpcMessage{}, err
		}
		line = append(line, chunk...)
		if len(line) > MaxFrame {
			return rpcMessage{}, ErrFrameTooLarge
		}
		if !isPrefix {
			break
		}
	}
	var m rpcMessage
	dec := json.NewDecoder(bytes.NewReader(line))
	if err := dec.Decode(&m); err != nil || m.JSONRPC != "2.0" {
		return rpcMessage{}, fmt.Errorf("malformed worker frame: %.200q", line)
	}
	return m, nil
}

func mustJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

type FactsParams struct {
	Kind     string `json:"kind"`
	Root     string `json:"root"`
	RepoRoot string `json:"repo_root"`
	Tree     string `json:"tree"`
	Project  string `json:"project"`
}

type FactsResult struct {
	Status      string         `json:"status"`
	Tree        string         `json:"tree"`
	Project     string         `json:"project"`
	Facts       *finding.Facts `json:"facts"`
	Limitations []string       `json:"limitations,omitempty"`
}

func (w *Worker) Facts(ctx context.Context, p FactsParams) (*FactsResult, error) {
	var res FactsResult
	if err := w.Call(ctx, "facts", p, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
