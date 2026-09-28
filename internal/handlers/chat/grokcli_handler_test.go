package chat

import (
	json "encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"patunganrouter/proxy/internal/providers"
	"patunganrouter/proxy/internal/proxy/executor"
)

func TestForwardGrokCLIRequest_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "grok-shell/0.2.99 (linux; x86_64)" {
			t.Errorf("expected grok-shell User-Agent, got %q", r.Header.Get("User-Agent"))
		}
		if r.Header.Get("x-grok-client-identifier") != "grok-shell" {
			t.Errorf("expected x-grok-client-identifier header, got %q", r.Header.Get("x-grok-client-identifier"))
		}
		if r.Header.Get("x-grok-client-version") != "0.2.99" {
			t.Errorf("expected x-grok-client-version header, got %q", r.Header.Get("x-grok-client-version"))
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Authorization: Bearer test-key, got %q", r.Header.Get("Authorization"))
		}

		var reqBody map[string]any
		if err := json.UnmarshalRead(r.Body, &reqBody); err != nil {
			t.Fatalf("parse body: %v", err)
		}
		if reqBody["stream"] != true {
			t.Errorf("expected stream=true")
		}
		if reqBody["store"] != false {
			t.Errorf("expected store=false")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"grok response\"}\n\n" +
				"event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n" +
				"data: [DONE]\n\n",
		))
	}))
	defer srv.Close()

	cfg := &providers.ProviderConfig{
		BaseURL: srv.URL,
	}
	body := []byte(`{"model":"grok-build","messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	err := executor.ForwardGrokCLI(rec, &executor.Request{
		Client:   srv.Client(),
		Config:   cfg,
		APIKey:   "test-key",
		Body:     body,
		IsStream: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(rec.Body.String(), "grok response") {
		t.Errorf("expected response content, got %s", rec.Body.String())
	}
}

func TestForwardGrokCLIRequest_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	cfg := &providers.ProviderConfig{
		BaseURL: srv.URL,
	}
	body := []byte(`{"model":"x","messages":[]}`)
	rec := httptest.NewRecorder()
	err := executor.ForwardGrokCLI(rec, &executor.Request{
		Client:   srv.Client(),
		Config:   cfg,
		APIKey:   "bad-key",
		Body:     body,
		IsStream: true,
	})
	if err == nil {
		t.Fatal("expected error for 401")
	}
	var ue *upstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("expected *upstreamError, got %T", err)
	}
	if ue.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", ue.StatusCode)
	}
}

func TestGrokCLI_BaseURL_ResponsesEndpoint(t *testing.T) {
	cfg, ok := providers.KnownProviders["grok-cli"]
	if !ok {
		t.Fatal("expected grok-cli provider to be registered in KnownProviders")
	}
	expectedURL := "https://cli-chat-proxy.grok.com/v1/responses"
	if cfg.BaseURL != expectedURL {
		t.Errorf("expected grok-cli BaseURL %q, got %q", expectedURL, cfg.BaseURL)
	}
}

func TestForwardGrokCLIRequest_EndpointPath(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n[DONE]\n\n"))
	}))
	defer srv.Close()

	cfg := &providers.ProviderConfig{
		BaseURL: srv.URL + "/v1/responses",
	}
	body := []byte(`{"model":"grok-4.6","messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	err := executor.ForwardGrokCLI(rec, &executor.Request{
		Client:   srv.Client(),
		Config:   cfg,
		APIKey:   "test-key",
		Body:     body,
		IsStream: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedPath != "/v1/responses" {
		t.Errorf("expected request path /v1/responses, got %q", receivedPath)
	}
}
