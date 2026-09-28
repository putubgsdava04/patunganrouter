package dashboard

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"patunganrouter/proxy/internal/config"
	"patunganrouter/proxy/internal/handlerutil"
)

var unixTailscaleCandidates = []string{
	"/usr/local/bin/tailscale",
	"/opt/homebrew/bin/tailscale",
	"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
	"/usr/sbin/tailscale",
	"/usr/bin/tailscale",
	"/snap/bin/tailscale",
}

// findTailscaleBin searches for the tailscale executable across PATH and well-known locations.
func findTailscaleBin() string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	for _, cand := range unixTailscaleCandidates {
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	if runtime.GOOS == "windows" {
		candidates := []string{
			`C:\Program Files\Tailscale\tailscale.exe`,
			`C:\Program Files (x86)\Tailscale\tailscale.exe`,
		}
		for _, cand := range candidates {
			if _, err := os.Stat(cand); err == nil {
				return cand
			}
		}
	}
	return ""
}

// hasBrew checks if Homebrew is available on macOS.
func hasBrew() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	if _, err := exec.LookPath("brew"); err == nil {
		return true
	}
	for _, p := range []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// TailscaleStatusJSON represents the structure output by `tailscale status --json`.
type TailscaleStatusJSON struct {
	BackendState string `json:"BackendState"`
	AuthURL      string `json:"AuthURL"`
	Self         struct {
		DNSName string `json:"DNSName"`
		Online  bool   `json:"Online"`
	} `json:"Self"`
}

// probeTailscaleStatus executes `tailscale status --json` with a short timeout.
func probeTailscaleStatus(ctx context.Context, bin string) (*TailscaleStatusJSON, error) {
	if bin == "" {
		return nil, errors.New("tailscale binary not found")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "status", "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var st TailscaleStatusJSON
	if err := json.Unmarshal(out, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// HandleTunnelStatus handles GET /api/tunnel/status.
// Returns current state of Cloudflare Tunnel and Tailscale Funnel.
func (h *DashboardHandler) HandleTunnelStatus(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Repo.GetSettingsRaw()
	if err != nil || raw == nil {
		raw = map[string]any{}
	}

	tunnelEnabled, _ := raw["tunnelEnabled"].(bool)
	tunnelURL, _ := raw["tunnelUrl"].(string)

	tailscaleEnabled, _ := raw["tailscaleEnabled"].(bool)
	tailscaleURL, _ := raw["tailscaleUrl"].(string)

	bin := findTailscaleBin()
	tailscaleRunning := false
	tailscaleLoggedIn := false

	if bin != "" {
		if st, err := probeTailscaleStatus(r.Context(), bin); err == nil && st != nil {
			tailscaleLoggedIn = st.BackendState == "Running"
			tailscaleRunning = tailscaleLoggedIn
			if tailscaleURL == "" && st.Self.DNSName != "" {
				tailscaleURL = "https://" + strings.TrimSuffix(st.Self.DNSName, ".")
			}
		}
	} else if tailscaleEnabled && tailscaleURL != "" {
		tailscaleRunning = true
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"tunnel": map[string]any{
			"enabled":         tunnelEnabled,
			"settingsEnabled": tunnelEnabled,
			"tunnelUrl":       tunnelURL,
			"shortId":         "",
			"publicUrl":       tunnelURL,
			"running":         tunnelEnabled && tunnelURL != "",
		},
		"tailscale": map[string]any{
			"enabled":         tailscaleEnabled,
			"settingsEnabled": tailscaleEnabled,
			"tunnelUrl":       tailscaleURL,
			"running":         tailscaleRunning && tailscaleEnabled,
			"loggedIn":        tailscaleLoggedIn,
		},
		"download": map[string]any{
			"downloading": false,
			"progress":    0,
		},
	})
}

// HandleTunnelEnable handles POST /api/tunnel/enable.
func (h *DashboardHandler) HandleTunnelEnable(w http.ResponseWriter, r *http.Request) {
	writePlainError(w, http.StatusBadRequest, "Cloudflare Tunnel service is not yet supported in patunganrouter. Use an external reverse proxy (e.g. ngrok, cloudflared, or caddy) pointing to http://127.0.0.1:20130")
}

// HandleTunnelDisable handles POST /api/tunnel/disable.
func (h *DashboardHandler) HandleTunnelDisable(w http.ResponseWriter, r *http.Request) {
	_ = h.Repo.UpdateSettingsRaw(map[string]any{
		"tunnelEnabled": false,
	})
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// HandleTailscaleCheck handles GET /api/tunnel/tailscale-check.
// Checks Tailscale installation, login state, and daemon status, matching upstream route.js.
func (h *DashboardHandler) HandleTailscaleCheck(w http.ResponseWriter, r *http.Request) {
	bin := findTailscaleBin()
	if bin == "" {
		handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
			"installed":     false,
			"loggedIn":      false,
			"platform":      runtime.GOOS,
			"brewAvailable": hasBrew(),
			"daemonRunning": false,
		})
		return
	}

	st, err := probeTailscaleStatus(r.Context(), bin)
	daemonRunning := err == nil && st != nil
	loggedIn := daemonRunning && st.BackendState == "Running"

	tunnelURL := ""
	if loggedIn && st.Self.DNSName != "" {
		tunnelURL = "https://" + strings.TrimSuffix(st.Self.DNSName, ".")
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"installed":     true,
		"loggedIn":      loggedIn,
		"platform":      runtime.GOOS,
		"brewAvailable": hasBrew(),
		"daemonRunning": daemonRunning,
		"tunnelUrl":     tunnelURL,
	})
}

// HandleTailscaleEnable handles POST /api/tunnel/tailscale-enable.
// Mirrors upstream enableTailscale: verifies login, triggers login flow if needed, and starts background Funnel.
func (h *DashboardHandler) HandleTailscaleEnable(w http.ResponseWriter, r *http.Request) {
	bin := findTailscaleBin()
	if bin == "" {
		writePlainError(w, http.StatusBadRequest, "Tailscale CLI is not installed or detected on your system path")
		return
	}

	st, err := probeTailscaleStatus(r.Context(), bin)
	if err != nil || st == nil || st.BackendState != "Running" {
		authURL := ""
		if st != nil && st.AuthURL != "" {
			authURL = st.AuthURL
		} else {
			// Trigger login flow to obtain AuthURL
			upCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			out, _ := exec.CommandContext(upCtx, bin, "up", "--reset").CombinedOutput()
			re := regexp.MustCompile(`https://login\.tailscale\.com/[^\s]+`)
			if match := re.FindString(string(out)); match != "" {
				authURL = match
			}
		}

		if authURL != "" {
			handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
				"success":    false,
				"needsLogin": true,
				"authUrl":    authURL,
			})
			return
		}

		writePlainError(w, http.StatusBadRequest, "Tailscale daemon is not running or logged in. Run 'tailscale up' to log in.")
		return
	}

	// Reset any existing funnel before starting a fresh one
	_ = exec.Command(bin, "funnel", "--bg", "reset").Run()

	port := config.LoadConfig().Port
	if port <= 0 {
		port = 20130
	}

	funnelCtx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(funnelCtx, bin, "funnel", "--bg", strconv.Itoa(port))
	out, err := cmd.CombinedOutput()
	outStr := string(out)

	if strings.Contains(outStr, "Funnel is not enabled") {
		enableURL := "https://login.tailscale.com/admin/settings/features"
		re := regexp.MustCompile(`https://login\.tailscale\.com/[^\s]+`)
		if match := re.FindString(outStr); match != "" {
			enableURL = match
		}
		handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
			"success":          false,
			"funnelNotEnabled": true,
			"enableUrl":        enableURL,
		})
		return
	}

	if err != nil && !strings.Contains(outStr, "available at") {
		writePlainError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to start Tailscale funnel: %s", strings.TrimSpace(outStr)))
		return
	}

	// Retrieve actual public DNS name from status
	stFresh, err := probeTailscaleStatus(r.Context(), bin)
	var tunnelURL string
	if err == nil && stFresh != nil && stFresh.Self.DNSName != "" {
		tunnelURL = "https://" + strings.TrimSuffix(stFresh.Self.DNSName, ".")
	} else if st.Self.DNSName != "" {
		tunnelURL = "https://" + strings.TrimSuffix(st.Self.DNSName, ".")
	}

	if tunnelURL == "" {
		writePlainError(w, http.StatusInternalServerError, "Tailscale funnel started but failed to retrieve DNS name")
		return
	}

	_ = h.Repo.UpdateSettingsRaw(map[string]any{
		"tailscaleEnabled": true,
		"tailscaleUrl":     tunnelURL,
	})

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"tunnelUrl": tunnelURL,
	})
}

// HandleTailscaleDisable handles POST /api/tunnel/tailscale-disable.
// Mirrors upstream disableTailscale: resets background funnel and clears settings.
func (h *DashboardHandler) HandleTailscaleDisable(w http.ResponseWriter, r *http.Request) {
	bin := findTailscaleBin()
	if bin != "" {
		_ = exec.Command(bin, "funnel", "--bg", "reset").Run()
	}

	_ = h.Repo.UpdateSettingsRaw(map[string]any{
		"tailscaleEnabled": false,
		"tailscaleUrl":     "",
	})

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}
