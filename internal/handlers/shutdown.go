package handlers

import (
	"net/http"
	"os"
	"syscall"
	"time"

	"patunganrouter/proxy/internal/handlerutil"
	"patunganrouter/proxy/internal/shutdown"
)

// HandleShutdown handles POST /api/version/shutdown: acknowledges the request
// and then asks this process to stop, so the operator can release file locks or
// restart the gateway from the dashboard.
//
// The reply is written first and the signal is sent a moment later, mirroring
// the Next dashboard's deferred process.exit — otherwise the connection would
// reset before the UI sees the response.
func HandleShutdown(w http.ResponseWriter, r *http.Request) {
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Shutting down...",
	})

	go func() {
		time.Sleep(500 * time.Millisecond)
		// Unblock SSE streams before signalling: app.Run then runs fxApp.Stop,
		// which shuts the HTTP server down gracefully.
		shutdown.Cancel()
		if proc, err := os.FindProcess(os.Getpid()); err == nil {
			_ = proc.Signal(syscall.SIGTERM)
		}
	}()
}
