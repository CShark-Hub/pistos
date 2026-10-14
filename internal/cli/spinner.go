package cli

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinner redraws one status line on an interactive terminal until stopped, then erases it so the
// report that follows starts on a clean line.
type spinner struct {
	w     io.Writer
	mu    sync.Mutex
	label string
	stop  chan struct{}
	done  chan struct{}
}

// startSpinner animates label on w, or returns nil when w is not an interactive terminal; every
// method is safe on a nil spinner.
func startSpinner(w io.Writer, label string) *spinner {
	if !isTerminal(w) || os.Getenv("TERM") == "dumb" {
		return nil
	}
	s := &spinner{w: w, label: label, stop: make(chan struct{}), done: make(chan struct{})}
	go s.run()
	return s
}

func (s *spinner) run() {
	defer close(s.done)
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for i := 0; ; i++ {
		s.mu.Lock()
		frame := spinnerFrames[i%len(spinnerFrames)]
		if useColor() {
			frame = "\x1b[36m" + frame + ansiReset
		}
		fmt.Fprintf(s.w, "\r\x1b[K%s %s", frame, s.label)
		s.mu.Unlock()
		select {
		case <-s.stop:
			fmt.Fprint(s.w, "\r\x1b[K")
			return
		case <-t.C:
		}
	}
}

// Set replaces the label shown next to the spinner.
func (s *spinner) Set(label string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.label = label
	s.mu.Unlock()
}

// Stop erases the spinner line and waits until it is gone.
func (s *spinner) Stop() {
	if s == nil {
		return
	}
	close(s.stop)
	<-s.done
}
