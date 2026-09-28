package proxy

import (
	"context"
	"io"
	"testing"
	"time"

	"patunganrouter/proxy/internal/shutdown"
)

// TestStallReaderAbortsOnShutdown verifies that starting shutdown closes the
// underlying reader, unblocking a pending Read so in-flight streams end fast.
func TestStallReaderAbortsOnShutdown(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()

	r := NewStallReader(pr, time.Minute, "test")
	defer r.Close()

	shutdown.TestReset()
	defer shutdown.TestReset()
	shutdown.Cancel()
	_, err := r.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("expected Read to error after shutdown")
	}
}

func TestStallReaderAbortsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	defer pw.Close()

	r := NewStallReaderWithContext(ctx, pr, time.Minute, "test-cancel")
	defer r.Close()

	cancel()
	time.Sleep(20 * time.Millisecond)

	_, err := r.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("expected Read to error after context cancel")
	}
}
