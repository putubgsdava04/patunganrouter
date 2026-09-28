package handlers

import (
	json "encoding/json/v2"
	"io"
	"net/http"
	"strconv"
	"time"

	"patunganrouter/proxy/internal/handlerutil"
	"patunganrouter/proxy/internal/log"
	"patunganrouter/proxy/internal/shutdown"
	"patunganrouter/proxy/internal/tracing"
)

// consolePingInterval matches the Next dashboard's 25s keepalive so proxies
// that idle-out connections keep the SSE stream alive.
const consolePingInterval = 25 * time.Second

// HandleConsoleLogsGet returns the buffered console logs (translator
// console-logs GET). Matches Next's { success, logs } shape.
func HandleConsoleLogsGet(w http.ResponseWriter, r *http.Request) {
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"logs":    log.ConsoleLogs(),
	})
}

// HandleDebugTraces returns recent request spans + p50/p95/p99 latency per
// provider+model. Optional ?n= query caps the span list.
func HandleDebugTraces(w http.ResponseWriter, r *http.Request) {
	n := 0
	if q := r.URL.Query().Get("n"); q != "" {
		if v, err := strconv.Atoi(q); err == nil && v > 0 {
			n = v
		}
	}
	body, err := tracing.JSON(n)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// HandleConsoleLogsDelete clears the buffered console logs.
func HandleConsoleLogsDelete(w http.ResponseWriter, r *http.Request) {
	log.ClearConsoleLogs()
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// HandleConsoleLogsLevelGet returns the current server log level so the
// dashboard console-log page can show it without a restart.
func HandleConsoleLogsLevelGet(w http.ResponseWriter, r *http.Request) {
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"level":   log.LevelString(),
	})
}

// HandleConsoleLogsLevelPut changes the server log level at runtime
// (no restart needed). Body: {"level": "debug"|"info"|"warn"|"error"}.
func HandleConsoleLogsLevelPut(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	var req struct {
		Level string `json:"level"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Level == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing log level")
		return
	}
	lvl, ok := log.ParseLevel(req.Level)
	if !ok {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid log level (debug, info, warn, error)")
		return
	}
	log.SetLevel(lvl)
	level := log.LevelString()
	log.Info("server", "log level changed", "level", level)
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"level":   level,
	})
}

// HandleConsoleLogsStream streams live console output over SSE. On connect it
// sends the buffered logs as an "init" event, then "line" events as they
// arrive and a "clear" event on buffer clear, with a keepalive ping every 25s.
func HandleConsoleLogsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(v map[string]any) bool {
		b, err := json.Marshal(v)
		if err != nil {
			return false
		}
		if _, err := w.Write([]byte("data: " + string(b) + "\n\n")); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// Buffered logs first.
	if buffered := log.ConsoleLogs(); len(buffered) > 0 {
		if !send(map[string]any{"type": "init", "logs": buffered}) {
			return
		}
	}

	ch, cancel := log.SubscribeConsole()
	defer cancel()

	ping := time.NewTicker(consolePingInterval)
	defer ping.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-shutdown.Done():
			// Server is stopping: close the endless stream now instead of
			// holding server.Shutdown until its deadline (the browser tab
			// behind this connection never goes idle on its own).
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			switch ev.Kind() {
			case "clear":
				if !send(map[string]any{"type": "clear"}) {
					return
				}
			default:
				if !send(map[string]any{"type": "line", "line": ev.Line()}) {
					return
				}
			}
		case <-ping.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
