// Package lsp is an on-demand Language Server Protocol 3.17 client. It uses standard
// Content-Length framing, unlike the newline-delimited pack worker protocol.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"example.com/pistos/internal/runner"
)

const maxMessage = 64 << 20

var (
	ErrServerExited = errors.New("language server exited")
	ErrTooLarge     = errors.New("language server message exceeds size limit")
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

type ResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("language server error %d: %s", e.Code, e.Message)
}

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type Diagnostic struct {
	Range    Range           `json:"range"`
	Severity int             `json:"severity"`
	Code     json.RawMessage `json:"code,omitempty"`
	Source   string          `json:"source,omitempty"`
	Message  string          `json:"message"`
}

// Client is one running language server bound to a workspace root.
type Client struct {
	proc    *runner.Proc
	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int
	pending map[string]chan message
	readErr error
	done    chan struct{}

	Capabilities ServerCapabilities
	ServerInfo   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	Encoding string
	root     string

	settings   map[string]any
	diags      map[string]publishDiagnostics
	diagSignal chan struct{}
	progress   map[string]bool
	progressCh chan struct{}
	versions   map[string]int
}

type publishDiagnostics struct {
	URI         string       `json:"uri"`
	Version     *int         `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// ServerCapabilities keeps the subset pistos relies on; absent providers mean unsupported.
type ServerCapabilities struct {
	PositionEncoding       string          `json:"positionEncoding,omitempty"`
	DefinitionProvider     json.RawMessage `json:"definitionProvider,omitempty"`
	ReferencesProvider     json.RawMessage `json:"referencesProvider,omitempty"`
	ImplementationProvider json.RawMessage `json:"implementationProvider,omitempty"`
	DiagnosticProvider     json.RawMessage `json:"diagnosticProvider,omitempty"`
}

func provided(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "false" && s != "null"
}

func (c ServerCapabilities) Supports(method string) bool {
	switch method {
	case "textDocument/definition":
		return provided(c.DefinitionProvider)
	case "textDocument/references":
		return provided(c.ReferencesProvider)
	case "textDocument/implementation":
		return provided(c.ImplementationProvider)
	case "textDocument/diagnostic":
		return provided(c.DiagnosticProvider)
	}
	return false
}

// Start launches the server and performs initialize/initialized against root.
// settings answer workspace/configuration requests by top-level section.
func Start(ctx context.Context, cmd runner.Cmd, root string, initOptions json.RawMessage, settings map[string]any) (*Client, error) {
	proc, err := runner.Start(cmd)
	if err != nil {
		return nil, err
	}
	c := &Client{
		proc: proc, pending: map[string]chan message{}, done: make(chan struct{}), root: root,
		diags: map[string]publishDiagnostics{}, diagSignal: make(chan struct{}, 1),
		progress: map[string]bool{}, progressCh: make(chan struct{}, 1), versions: map[string]int{},
		settings: settings,
	}
	go c.readLoop(bufio.NewReaderSize(proc.Stdout, 64<<10))

	params := map[string]any{
		"processId":  os.Getpid(),
		"clientInfo": map[string]string{"name": "pistos"},
		"rootUri":    PathToURI(root),
		"workspaceFolders": []map[string]string{
			{"uri": PathToURI(root), "name": filepath.Base(root)},
		},
		"capabilities": map[string]any{
			"general": map[string]any{"positionEncodings": []string{"utf-8", "utf-16"}},
			"window":  map[string]any{"workDoneProgress": true},
			"workspace": map[string]any{
				"configuration":    true,
				"workspaceFolders": true,
			},
			"textDocument": map[string]any{
				"synchronization":    map[string]any{"didSave": false},
				"definition":         map[string]any{"linkSupport": false},
				"references":         map[string]any{},
				"implementation":     map[string]any{"linkSupport": false},
				"diagnostic":         map[string]any{"dynamicRegistration": false},
				"publishDiagnostics": map[string]any{"versionSupport": true},
			},
		},
	}
	if len(initOptions) > 0 {
		params["initializationOptions"] = initOptions
	}
	var res struct {
		Capabilities ServerCapabilities `json:"capabilities"`
		ServerInfo   *struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := c.Call(ctx, "initialize", params, &res); err != nil {
		c.Kill()
		return nil, fmt.Errorf("initialize failed: %w", err)
	}
	c.Capabilities = res.Capabilities
	if res.ServerInfo != nil {
		c.ServerInfo = *res.ServerInfo
	}
	c.Encoding = res.Capabilities.PositionEncoding
	if c.Encoding == "" {
		c.Encoding = "utf-16"
	}
	if err := c.Notify("initialized", map[string]any{}); err != nil {
		c.Kill()
		return nil, err
	}
	return c, nil
}

func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	if c.readErr != nil {
		c.mu.Unlock()
		return c.readErr
	}
	c.nextID++
	id := strconv.Itoa(c.nextID)
	ch := make(chan message, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.write(message{JSONRPC: "2.0", ID: json.RawMessage(id), Method: method, Params: mustJSON(params)}); err != nil {
		return err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if result != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, result)
		}
		return nil
	case <-c.done:
		return c.readErr
	case <-ctx.Done():
		_ = c.Notify("$/cancelRequest", map[string]any{"id": c.nextIDValue(id)})
		return ctx.Err()
	}
}

func (c *Client) nextIDValue(id string) int {
	n, _ := strconv.Atoi(id)
	return n
}

func (c *Client) Notify(method string, params any) error {
	return c.write(message{JSONRPC: "2.0", Method: method, Params: mustJSON(params)})
}

func (c *Client) write(m message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := fmt.Fprintf(c.proc.Stdin, "Content-Length: %d\r\n\r\n", len(b)); err != nil {
		return err
	}
	_, err = c.proc.Stdin.Write(b)
	return err
}

func (c *Client) readLoop(r *bufio.Reader) {
	tp := textproto.NewReader(r)
	fail := func(err error) {
		c.mu.Lock()
		c.readErr = err
		c.mu.Unlock()
		close(c.done)
	}
	for {
		header, err := tp.ReadMIMEHeader()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				fail(ErrServerExited)
			} else {
				fail(fmt.Errorf("malformed language server header: %w", err))
			}
			return
		}
		n, err := strconv.Atoi(header.Get("Content-Length"))
		if err != nil || n < 0 {
			fail(fmt.Errorf("language server sent an invalid Content-Length %q", header.Get("Content-Length")))
			return
		}
		if n > maxMessage {
			fail(ErrTooLarge)
			return
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			fail(ErrServerExited)
			return
		}
		var m message
		if err := json.Unmarshal(body, &m); err != nil {
			fail(fmt.Errorf("malformed language server message: %.200q", body))
			return
		}
		c.dispatch(m)
	}
}

func (c *Client) dispatch(m message) {
	switch {
	case m.Method == "" && len(m.ID) > 0:
		c.mu.Lock()
		ch := c.pending[strings.Trim(string(m.ID), `"`)]
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	case len(m.ID) > 0:
		c.answerServerRequest(m)
	case m.Method == "textDocument/publishDiagnostics":
		var p publishDiagnostics
		if json.Unmarshal(m.Params, &p) == nil {
			c.mu.Lock()
			c.diags[normalizeURI(p.URI)] = p
			c.mu.Unlock()
			signal(c.diagSignal)
		}
	case m.Method == "$/progress":
		var p struct {
			Token json.RawMessage `json:"token"`
			Value struct {
				Kind string `json:"kind"`
			} `json:"value"`
		}
		if json.Unmarshal(m.Params, &p) == nil {
			c.mu.Lock()
			switch p.Value.Kind {
			case "begin":
				c.progress[string(p.Token)] = true
			case "end":
				delete(c.progress, string(p.Token))
			}
			c.mu.Unlock()
			signal(c.progressCh)
		}
	}
}

// answerServerRequest replies to requests a server sends to the client. Configuration requests get
// empty settings so servers fall back to the project's own configuration files.
func (c *Client) answerServerRequest(m message) {
	reply := message{JSONRPC: "2.0", ID: m.ID}
	switch m.Method {
	case "workspace/configuration":
		var p struct {
			Items []struct {
				Section string `json:"section"`
			} `json:"items"`
		}
		_ = json.Unmarshal(m.Params, &p)
		out := make([]any, len(p.Items))
		for i, it := range p.Items {
			out[i] = c.settings[it.Section]
		}
		reply.Result = mustJSON(out)
	case "workspace/workspaceFolders":
		reply.Result = mustJSON([]map[string]string{{"uri": PathToURI(c.root), "name": filepath.Base(c.root)}})
	case "window/workDoneProgress/create", "client/registerCapability", "client/unregisterCapability", "window/showMessageRequest":
		reply.Result = json.RawMessage("null")
	default:
		reply.Error = &ResponseError{Code: -32601, Message: "method not supported by pistos: " + m.Method}
	}
	_ = c.write(reply)
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// WaitIdle waits until every work-done progress the server started has ended. Servers that load
// projects asynchronously announce that work through progress; returning before it ends would
// produce answers from a partially loaded workspace.
func (c *Client) WaitIdle(ctx context.Context, settle time.Duration) error {
	for {
		c.mu.Lock()
		busy := len(c.progress)
		c.mu.Unlock()
		if busy == 0 {
			select {
			case <-c.progressCh:
				continue
			case <-time.After(settle):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			case <-c.done:
				return c.readErr
			}
		}
		select {
		case <-c.progressCh:
		case <-ctx.Done():
			return fmt.Errorf("workspace still loading: %w", ctx.Err())
		case <-c.done:
			return c.readErr
		}
	}
}

// Open sends didOpen for a file with its content; versions start at 1 per document.
func (c *Client) Open(path, languageID string) (string, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	uri := PathToURI(path)
	c.mu.Lock()
	c.versions[uri]++
	v := c.versions[uri]
	c.mu.Unlock()
	return uri, c.Notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": languageID, "version": v, "text": string(text)},
	})
}

// Locations runs a location request (definition, references, implementation).
func (c *Client) Locations(ctx context.Context, method, uri string, pos Position) ([]Location, error) {
	if !c.Capabilities.Supports(method) {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, method)
	}
	params := map[string]any{"textDocument": map[string]string{"uri": uri}, "position": pos}
	if method == "textDocument/references" {
		params["context"] = map[string]bool{"includeDeclaration": true}
	}
	var raw json.RawMessage
	if err := c.Call(ctx, method, params, &raw); err != nil {
		return nil, err
	}
	return decodeLocations(raw)
}

var ErrUnsupported = errors.New("server does not support")

func decodeLocations(raw json.RawMessage) ([]Location, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return nil, nil
	}
	if strings.HasPrefix(s, "{") {
		var l Location
		err := json.Unmarshal(raw, &l)
		return []Location{l}, err
	}
	var items []struct {
		Location
		TargetURI   string `json:"targetUri"`
		TargetRange *Range `json:"targetSelectionRange"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	out := make([]Location, 0, len(items))
	for _, it := range items {
		if it.TargetURI != "" && it.TargetRange != nil {
			out = append(out, Location{URI: it.TargetURI, Range: *it.TargetRange})
			continue
		}
		out = append(out, it.Location)
	}
	return out, nil
}

// DiagnosticsResult reports whether the server proved the list complete for the document.
type DiagnosticsResult struct {
	Diagnostics []Diagnostic
	Complete    bool
	Reason      string
}

// Diagnostics uses pull diagnostics when the server supports them. Push-only servers give no signal
// that analysis finished, so their diagnostics are returned as incomplete — never inferred from a
// sleep or from the first notification.
func (c *Client) Diagnostics(ctx context.Context, uri string, wait time.Duration) (DiagnosticsResult, error) {
	if c.Capabilities.Supports("textDocument/diagnostic") {
		var rep struct {
			Kind  string       `json:"kind"`
			Items []Diagnostic `json:"items"`
		}
		err := c.Call(ctx, "textDocument/diagnostic", map[string]any{"textDocument": map[string]string{"uri": uri}}, &rep)
		if err != nil {
			return DiagnosticsResult{}, err
		}
		if rep.Kind != "full" {
			return DiagnosticsResult{Diagnostics: rep.Items, Reason: "server returned an unchanged/partial report"}, nil
		}
		return DiagnosticsResult{Diagnostics: rep.Items, Complete: true}, nil
	}
	deadline := time.After(wait)
	for {
		c.mu.Lock()
		p, ok := c.diags[normalizeURI(uri)]
		c.mu.Unlock()
		if ok {
			return DiagnosticsResult{Diagnostics: p.Diagnostics, Reason: "server only pushes diagnostics; it gives no completion signal"}, nil
		}
		select {
		case <-c.diagSignal:
		case <-deadline:
			return DiagnosticsResult{Reason: "no diagnostics were published before the deadline"}, nil
		case <-ctx.Done():
			return DiagnosticsResult{}, ctx.Err()
		case <-c.done:
			return DiagnosticsResult{}, c.readErr
		}
	}
}

// Shutdown performs the shutdown/exit handshake and reaps the process tree.
func (c *Client) Shutdown(ctx context.Context) error {
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := c.Call(sctx, "shutdown", nil, nil)
	_ = c.Notify("exit", nil)
	c.proc.Stdin.Close()
	_ = c.proc.Wait(3 * time.Second)
	return err
}

func (c *Client) Kill()          { c.proc.Kill() }
func (c *Client) Pid() int       { return c.proc.Pid() }
func (c *Client) Stderr() []byte { return c.proc.Stderr() }

func PathToURI(p string) string {
	abs, _ := filepath.Abs(p)
	s := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" {
		s = "/" + s
	}
	return (&url.URL{Scheme: "file", Path: s}).String()
}

func URIToPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	p := u.Path
	if runtime.GOOS == "windows" && len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p), true
}

func normalizeURI(uri string) string {
	if p, ok := URIToPath(uri); ok {
		return PathToURI(p)
	}
	return uri
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
