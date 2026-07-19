package app

import (
	"fmt"
	"io"
	"sync"
	"time"
)

const cliLogTimeFormat = "2006-01-02 15:04:05"

type timestampWriter struct {
	mu          sync.Mutex
	output      io.Writer
	atLineStart bool
}

func newTimestampWriter(output io.Writer) io.Writer {
	if output == nil {
		return io.Discard
	}
	return &timestampWriter{output: output, atLineStart: true}
}

func NewTimestampWriter(output io.Writer) io.Writer {
	return newTimestampWriter(output)
}

func (w *timestampWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, b := range data {
		if w.atLineStart {
			if _, err := fmt.Fprintf(w.output, "[%s] ", time.Now().Format(cliLogTimeFormat)); err != nil {
				return 0, err
			}
			w.atLineStart = false
		}
		if _, err := w.output.Write([]byte{b}); err != nil {
			return 0, err
		}
		if b == '\n' {
			w.atLineStart = true
		}
	}
	return len(data), nil
}
