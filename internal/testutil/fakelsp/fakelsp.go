// Package fakelsp is a scriptable language server for client failure tests. Test binaries re-execute
// themselves with EnvMode set and call MaybeServe from TestMain.
package fakelsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/textproto"
	"os"
	"strconv"
	"time"
)

const EnvMode = "PISTOS_FAKE_LSP"

const (
	ModePull     = "pull"      // supports pull diagnostics
	ModePushLate = "push-late" // push-only, publishes after a delay
	ModeInitFail = "init-fail" // rejects initialize
	ModeCrash    = "crash"     // exits after initialize
	ModeLoading  = "loading"   // reports workspace loading that never ends
	ModeLoadDone = "load-done" // reports loading that ends after a delay, then answers
)

func MaybeServe() {
	if mode := os.Getenv(EnvMode); mode != "" {
		serve(mode)
		os.Exit(0)
	}
}

type msg struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func serve(mode string) {
	r := bufio.NewReader(os.Stdin)
	tp := textproto.NewReader(r)
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(b), b)
	}
	reply := func(id json.RawMessage, result any) {
		send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	for {
		h, err := tp.ReadMIMEHeader()
		if err != nil {
			return
		}
		n, _ := strconv.Atoi(h.Get("Content-Length"))
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		var m msg
		_ = json.Unmarshal(body, &m)
		switch m.Method {
		case "initialize":
			if mode == ModeInitFail {
				send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32603, "message": "cannot load workspace"}})
				continue
			}
			caps := map[string]any{"definitionProvider": true, "referencesProvider": true, "positionEncoding": "utf-8"}
			if mode == ModePull {
				caps["diagnosticProvider"] = map[string]any{"interFileDependencies": false, "workspaceDiagnostics": false}
			}
			reply(m.ID, map[string]any{"capabilities": caps, "serverInfo": map[string]string{"name": "fake", "version": "1"}})
		case "initialized":
			switch mode {
			case ModeCrash:
				os.Exit(7)
			case ModeLoading, ModeLoadDone:
				send(map[string]any{"jsonrpc": "2.0", "method": "$/progress", "params": map[string]any{"token": "load", "value": map[string]string{"kind": "begin", "title": "Loading"}}})
				if mode == ModeLoadDone {
					go func() {
						time.Sleep(300 * time.Millisecond)
						send(map[string]any{"jsonrpc": "2.0", "method": "$/progress", "params": map[string]any{"token": "load", "value": map[string]string{"kind": "end"}}})
					}()
				}
			}
		case "textDocument/didOpen":
			if mode == ModePushLate {
				var p struct {
					TextDocument struct {
						URI string `json:"uri"`
					} `json:"textDocument"`
				}
				_ = json.Unmarshal(m.Params, &p)
				go func() {
					time.Sleep(200 * time.Millisecond)
					send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": map[string]any{
						"uri": p.TextDocument.URI, "diagnostics": []any{map[string]any{"range": rng(0, 0), "severity": 1, "message": "late"}}}})
				}()
			}
		case "textDocument/definition":
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(m.Params, &p)
			reply(m.ID, []any{map[string]any{"uri": p.TextDocument.URI, "range": rng(2, 4)}, map[string]any{"uri": "file:///usr/lib/outside.go", "range": rng(0, 0)}})
		case "textDocument/diagnostic":
			reply(m.ID, map[string]any{"kind": "full", "items": []any{map[string]any{"range": rng(1, 2), "severity": 2, "code": "F1", "message": "pulled"}}})
		case "shutdown":
			reply(m.ID, nil)
		case "exit":
			return
		}
	}
}

func rng(line, char int) map[string]any {
	p := map[string]int{"line": line, "character": char}
	return map[string]any{"start": p, "end": p}
}
