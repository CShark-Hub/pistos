// Package fakeworker implements a scriptable pack worker for protocol and failure tests.
// Test binaries re-execute themselves with EnvMode set and call MaybeServe from TestMain.
package fakeworker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	EnvMode = "PISTOS_FAKE_WORKER"
	// EnvPidFile, when set, receives the pid of a grandchild so tests can prove tree cleanup.
	EnvPidFile = "PISTOS_FAKE_WORKER_PIDFILE"
)

// Modes understood by Serve.
const (
	ModeOK          = "ok"
	ModeBadProtocol = "bad-protocol"
	ModeMalformed   = "malformed"
	ModeHuge        = "huge"
	ModeCrash       = "crash"
	ModeHang        = "hang"
	ModeProgress    = "progress"
	ModeWrongScope  = "wrong-scope"
)

// MaybeServe runs the fake worker and exits if the environment requests it.
func MaybeServe() {
	mode := os.Getenv(EnvMode)
	if mode == "" {
		return
	}
	Serve(mode)
	os.Exit(0)
}

type msg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func Serve(mode string) {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	out := bufio.NewWriter(os.Stdout)
	write := func(v any) {
		b, _ := json.Marshal(v)
		out.Write(append(b, '\n'))
		out.Flush()
	}
	reply := func(id *int64, result any) {
		write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	fmt.Fprintln(os.Stderr, "fake worker started in mode", mode)

	for in.Scan() {
		var m msg
		if err := json.Unmarshal(in.Bytes(), &m); err != nil {
			continue
		}
		switch m.Method {
		case "initialize":
			var p struct {
				Pack        string `json:"pack"`
				PackVersion string `json:"pack_version"`
			}
			_ = json.Unmarshal(m.Params, &p)
			protocol := 1
			if mode == ModeBadProtocol {
				protocol = 99
			}
			reply(m.ID, map[string]any{"protocol": protocol, "pack": p.Pack, "pack_version": p.PackVersion, "capabilities": []string{"fake-lint"}})
		case "analyze":
			var p struct {
				Tree    string `json:"tree"`
				Project string `json:"project"`
			}
			_ = json.Unmarshal(m.Params, &p)
			switch mode {
			case ModeMalformed:
				out.WriteString("{not json\n")
				out.Flush()
				continue
			case ModeHuge:
				out.WriteString(`{"jsonrpc":"2.0","id":` + fmt.Sprint(*m.ID) + `,"result":"` + strings.Repeat("x", 17<<20) + "\"}\n")
				out.Flush()
				continue
			case ModeCrash:
				os.Exit(3)
			case ModeHang:
				spawnGrandchild()
				time.Sleep(time.Hour)
			case ModeProgress:
				for i := 0; i < 3; i++ {
					write(map[string]any{"jsonrpc": "2.0", "method": "progress", "params": map[string]any{"done": i}})
				}
			case ModeWrongScope:
				p.Tree = "some-other-tree"
			}
			reply(m.ID, map[string]any{
				"status": "complete", "tree": p.Tree, "project": p.Project,
				"diagnostics": []map[string]any{{"path": "main.go", "line": 3, "column": 2, "rule": "fake001", "severity": "warning", "message": "fake finding"}},
			})
		case "shutdown":
			reply(m.ID, nil)
			return
		}
	}
}

// spawnGrandchild starts a long sleep in the worker's process group to verify tree cleanup.
func spawnGrandchild() {
	cmd := exec.Command("sleep", "3600")
	if err := cmd.Start(); err != nil {
		return
	}
	if f := os.Getenv(EnvPidFile); f != "" {
		_ = os.WriteFile(f, []byte(fmt.Sprint(cmd.Process.Pid)), 0o644)
	}
}
