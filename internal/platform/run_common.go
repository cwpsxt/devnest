package platform

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
)

// lineWriter collects process output line by line. Both \r and \n end a line
// so progress output that redraws one line still shows up.
type lineWriter struct {
	mu     sync.Mutex
	buf    []byte
	all    strings.Builder
	onLine func(string)
	decode func([]byte) string
}

const maxKeptOutput = 256 << 10

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexAny(w.buf, "\r\n")
		if i < 0 {
			break
		}
		w.emit(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.emit(w.buf)
		w.buf = nil
	}
}

func (w *lineWriter) emit(b []byte) {
	if len(bytes.TrimSpace(b)) == 0 {
		return
	}
	s := string(b)
	if w.decode != nil {
		s = w.decode(b)
	}
	if w.all.Len() < maxKeptOutput {
		w.all.WriteString(s)
		w.all.WriteByte('\n')
	}
	if w.onLine != nil {
		w.onLine(s)
	}
}

func (w *lineWriter) output() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.all.String()
}

func codeOK(code int, ok []int) bool {
	if code == 0 {
		return true
	}
	for _, c := range ok {
		if c == code {
			return true
		}
	}
	return false
}

// ExitError is returned by Run when the program ran but reported failure.
type ExitError struct {
	Exe    string
	Code   int
	Output string
}

func (e *ExitError) Error() string {
	tail := strings.TrimSpace(e.Output)
	if lines := strings.Split(tail, "\n"); len(lines) > 6 {
		tail = strings.Join(lines[len(lines)-6:], "\n")
	}
	if tail == "" {
		return fmt.Sprintf("%s 退出码 %d", e.Exe, e.Code)
	}
	return fmt.Sprintf("%s 退出码 %d：%s", e.Exe, e.Code, tail)
}

// BatQuote quotes one argument for a cmd.exe command line. Batch files split
// arguments on spaces, commas, semicolons and equals signs, so anything that
// contains one of those is wrapped in double quotes.
func BatQuote(s string, force bool) string {
	if s == "" {
		return `""`
	}
	if !force && !strings.ContainsAny(s, " \t&|<>^()=;,!%\"") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
