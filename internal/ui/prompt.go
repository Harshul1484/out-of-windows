package ui

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Confirm asks a yes/no question. Anything other than y/yes is "no", and so
// is end of input.
func Confirm(in io.Reader, out io.Writer, question string) bool {
	fmt.Fprintf(out, "%s %s ", question, Muted.Render("[y/N]"))
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(out)
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// ConfirmTyped asks the user to type an exact word (e.g. "delete") for
// actions that cannot be undone.
func ConfirmTyped(in io.Reader, out io.Writer, question, word string) bool {
	fmt.Fprintf(out, "%s\nType %s to continue: ", question, Key.Render(word))
	line, _ := bufio.NewReader(in).ReadString('\n')
	return strings.TrimSpace(line) == word
}

// Spinner shows an animated status line on a terminal. On non-terminals it
// prints nothing. Status is polled from a function so callers can report
// live counters without synchronisation of their own.
type Spinner struct {
	out    io.Writer
	status func() string
	stop   chan struct{}
	done   sync.WaitGroup
	active bool
}

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// StartSpinner starts a spinner if enabled is true.
func StartSpinner(out io.Writer, enabled bool, status func() string) *Spinner {
	s := &Spinner{out: out, status: status, stop: make(chan struct{})}
	if !enabled {
		return s
	}
	s.active = true
	s.done.Add(1)
	go func() {
		defer s.done.Done()
		t := time.NewTicker(90 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			line := Accent.Render(spinFrames[i%len(spinFrames)]) + " " + TruncateMiddle(s.status(), Width()-4)
			fmt.Fprintf(s.out, "\r\x1b[2K%s", line)
			select {
			case <-s.stop:
				fmt.Fprint(s.out, "\r\x1b[2K")
				return
			case <-t.C:
			}
		}
	}()
	return s
}

// Stop clears the spinner line.
func (s *Spinner) Stop() {
	if s.active {
		close(s.stop)
		s.done.Wait()
		s.active = false
	}
}
