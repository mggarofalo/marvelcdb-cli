package cli

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// captureStdout redirects os.Stdout, which is where the printer writes, and
// returns a function that restores it and yields what was written.
func captureStdout(t *testing.T) (*os.File, func(*os.File) string) {
	t.Helper()

	original := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	return original, func(restore *os.File) string {
		_ = w.Close()
		os.Stdout = restore
		out := <-done
		_ = r.Close()
		return out
	}
}
