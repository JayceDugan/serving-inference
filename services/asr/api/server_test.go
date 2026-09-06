package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- stub upstreams ---------------------------------------------------------

type stubASR struct {
	srv *httptest.Server

	calls atomic.Int32
}

func newStubASR(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *stubASR {
	s := &stubASR{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		if r.URL.Path != "/v1/audio/transcriptions" {
			http.NotFound(w, r)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func stubASROK(text string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": text})
	}
}

type stubCleanup struct {
	srv *httptest.Server

	calls     atomic.Int32
	lastBody  atomic.Value // []byte JSON body
	respCode  int
	respText  string
	handlerFn func(w http.ResponseWriter, r *http.Request)
}

func newStubCleanup(t *testing.T) *stubCleanup {
	c := &stubCleanup{respCode: http.StatusOK, respText: "cleaned"}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		c.lastBody.Store(body)
		if c.handlerFn != nil {
			c.handlerFn(w, r)
			return
		}
		if c.respCode != http.StatusOK {
			http.Error(w, "cleanup down", c.respCode)
			return
		}
		out := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": c.respText}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

// --- test harness -----------------------------------------------------------

func newTestServer(t *testing.T, asrURL, cleanupURL string, mutate func(*Config)) (*httptest.Server, Config) {
	t.Helper()
	prompt, err := os.ReadFile("../prompts/cleanup-system.txt")
	if err != nil {
		t.Fatalf("loading cleanup prompt: %v", err)
	}
	cfg := Config{
		ListenAddr:       ":0",
		ASRBaseURL:       asrURL + "/v1",
		CleanupBaseURL:   cleanupURL + "/v1",
		ASRModelName:     "qwen3-asr",
		CleanupModelName: "s1-mini",
		MaxAudioBytes:    25 << 20,
		UpstreamTimeout:  5 * time.Second,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	httpClient := &http.Client{}
	api := NewServer(cfg,
		&ASRClient{BaseURL: cfg.ASRBaseURL, Model: cfg.ASRModelName, HTTP: httpClient},
		&CleanupClient{BaseURL: cfg.CleanupBaseURL, Model: cfg.CleanupModelName,
			SystemPrompt: string(prompt), HTTP: httpClient},
	)
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return srv, cfg
}

// postTranscribe sends a multipart request. Passing fields with the key "file"
// (empty value allowed) forces creation of a zero-byte file part.
func postTranscribe(t *testing.T, url string, audio []byte, fields map[string]string, headers map[string]string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_, forceFile := fields["file"]
	if len(audio) > 0 || forceFile {
		p, err := mw.CreateFormFile("file", "take.wav")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = p.Write(audio)
	}
	for k, v := range fields {
		if k == "file" {
			continue
		}
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func decodeTranscribe(t *testing.T, resp *http.Response) TranscribeResponse {
	t.Helper()
	var out TranscribeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func decodeError(t *testing.T, resp *http.Response) errorBody {
	t.Helper()
	var out errorBody
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return out
}

// --- tests ------------------------------------------------------------------

func TestHealthz(t *testing.T) {
	asr := newStubASR(t, stubASROK("x"))
	cu := newStubCleanup(t)
	srv, _ := newTestServer(t, asr.srv.URL, cu.srv.URL, nil)

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("body = %v", body)
	}
}

func TestTranscribeSuccess(t *testing.T) {
	const rawText = "um so the weather is nice today"
	const cleaned = "So the weather is nice today."

	var forwardedBody atomic.Value
	asr := newStubASR(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		forwardedBody.Store(body)
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/form-data") {
			t.Errorf("asr stub: content-type = %q", ct)
		}
		s := string(body)
		for _, want := range []string{`name="model"`, "qwen3-asr", `filename="take.wav"`, `name="language"`, "en", `name="prompt"`, "medical terms"} {
			if !strings.Contains(s, want) {
				t.Errorf("forwarded ASR body missing %q", want)
			}
		}
		// Sleep so millisecond timings are observably nonzero.
		time.Sleep(2 * time.Millisecond)
		stubASROK(rawText)(w, r)
	})

	cu := newStubCleanup(t)
	cu.respText = cleaned

	srv, _ := newTestServer(t, asr.srv.URL, cu.srv.URL, nil)

	audio := []byte("RIFFfake-wav-bytes")
	resp := postTranscribe(t, srv.URL+"/v1/transcribe", audio,
		map[string]string{"language": "en", "prompt": "medical terms"}, nil)

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (body: %s)", resp.StatusCode, b)
	}
	got := decodeTranscribe(t, resp)

	if got.RawText != rawText {
		t.Errorf("raw_text = %q, want %q", got.RawText, rawText)
	}
	if got.Text != cleaned {
		t.Errorf("text = %q, want %q", got.Text, cleaned)
	}
	if !got.CleanupApplied {
		t.Error("cleanup_applied = false, want true")
	}
	if got.Warning != "" {
		t.Errorf("unexpected warning: %q", got.Warning)
	}
	if asr.calls.Load() != 1 || cu.calls.Load() != 1 {
		t.Errorf("upstream calls: asr=%d cleanup=%d, want 1/1", asr.calls.Load(), cu.calls.Load())
	}

	// The exact audio bytes must reach the ASR upstream.
	if !bytes.Contains(forwardedBody.Load().([]byte), audio) {
		t.Error("audio bytes not forwarded verbatim to ASR upstream")
	}

	// Cleanup upstream must receive a chat body containing the raw transcript.
	var chatReq struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Temperature        *float64       `json:"temperature"`
		MaxTokens          int            `json:"max_tokens"`
		ChatTemplateKwargs map[string]any `json:"chat_template_kwargs"`
	}
	if err := json.Unmarshal(cu.lastBody.Load().([]byte), &chatReq); err != nil {
		t.Fatalf("cleanup body: %v", err)
	}
	if chatReq.Model != "s1-mini" {
		t.Errorf("cleanup model = %q", chatReq.Model)
	}
	if len(chatReq.Messages) != 2 || chatReq.Messages[0].Role != "system" || chatReq.Messages[1].Role != "user" {
		t.Fatalf("cleanup messages = %+v", chatReq.Messages)
	}
	wantUser := "[Styling: semi-formal] [Structure: prose] [Context: general]\n" + rawText
	if chatReq.Messages[1].Content != wantUser {
		t.Errorf("cleanup user message = %q, want control line + raw transcript", chatReq.Messages[1].Content)
	}
	if !strings.Contains(chatReq.Messages[0].Content, "speech-to-text") {
		t.Error("system prompt does not describe the post-processing task")
	}
	if chatReq.Temperature == nil || *chatReq.Temperature != 0 {
		t.Errorf("cleanup temperature = %v, want 0", chatReq.Temperature)
	}
	if v, ok := chatReq.ChatTemplateKwargs["enable_thinking"]; !ok || v != false {
		t.Errorf("chat_template_kwargs.enable_thinking = %v, want false", v)
	}
	if chatReq.MaxTokens < 64 {
		t.Errorf("max_tokens = %d, want >= 64", chatReq.MaxTokens)
	}

	if got.Timings.Total < 1 || got.Timings.ASR < 1 {
		t.Errorf("timings = %+v, want asr/total >= 1ms (stub sleeps 2ms)", got.Timings)
	}
}

func TestTranscribeCleanupDisabled(t *testing.T) {
	asr := newStubASR(t, stubASROK("raw words"))
	cu := newStubCleanup(t)
	srv, _ := newTestServer(t, asr.srv.URL, cu.srv.URL, nil)

	resp := postTranscribe(t, srv.URL+"/v1/transcribe", []byte("audio"), map[string]string{"cleanup": "false"}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := decodeTranscribe(t, resp)
	if got.Text != "raw words" || got.CleanupApplied {
		t.Errorf("got %+v, want raw passthrough", got)
	}
	if cu.calls.Load() != 0 {
		t.Errorf("cleanup called %d times with cleanup=false", cu.calls.Load())
	}
}

func TestTranscribeCleanupOffValues(t *testing.T) {
	for _, v := range []string{"false", "False", "0", "off", "no"} {
		t.Run(v, func(t *testing.T) {
			asr := newStubASR(t, stubASROK("raw"))
			cu := newStubCleanup(t)
			srv, _ := newTestServer(t, asr.srv.URL, cu.srv.URL, nil)
			resp := postTranscribe(t, srv.URL+"/v1/transcribe", []byte("a"), map[string]string{"cleanup": v}, nil)
			if resp.StatusCode != 200 {
				t.Fatalf("status %d", resp.StatusCode)
			}
			_ = decodeTranscribe(t, resp)
			if cu.calls.Load() != 0 {
				t.Errorf("cleanup=%q should disable cleanup", v)
			}
		})
	}
	for _, v := range []string{"true", "1", "on", "yes", "", "garbage"} {
		t.Run("keep:"+v, func(t *testing.T) {
			asr := newStubASR(t, stubASROK("raw"))
			cu := newStubCleanup(t)
			srv, _ := newTestServer(t, asr.srv.URL, cu.srv.URL, nil)
			postTranscribe(t, srv.URL+"/v1/transcribe", []byte("a"), map[string]string{"cleanup": v}, nil)
			if cu.calls.Load() != 1 {
				t.Errorf("cleanup=%q should keep cleanup enabled (calls=%d)", v, cu.calls.Load())
			}
		})
	}
}

func TestTranscribeASRUpstreamError(t *testing.T) {
	asr := newStubASR(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "CUDA out of memory", http.StatusInternalServerError)
	})
	cu := newStubCleanup(t)
	srv, _ := newTestServer(t, asr.srv.URL, cu.srv.URL, nil)

	resp := postTranscribe(t, srv.URL+"/v1/transcribe", []byte("audio"), nil, nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	e := decodeError(t, resp)
	if e.Error.Type != "upstream_error" {
		t.Errorf("error type = %q", e.Error.Type)
	}
	if !strings.Contains(e.Error.Message, "asr") {
		t.Errorf("error message should name the failing service: %q", e.Error.Message)
	}
	if cu.calls.Load() != 0 {
		t.Error("cleanup must not run when ASR failed")
	}
}

func TestTranscribeASRMalformedJSON(t *testing.T) {
	asr := newStubASR(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"not_text":`))
	})
	cu := newStubCleanup(t)
	srv, _ := newTestServer(t, asr.srv.URL, cu.srv.URL, nil)

	resp := postTranscribe(t, srv.URL+"/v1/transcribe", []byte("audio"), nil, nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if cu.calls.Load() != 0 {
		t.Error("cleanup must not run on malformed ASR response")
	}
}

func TestTranscribeCleanupFailureFallsBackToRaw(t *testing.T) {
	asr := newStubASR(t, stubASROK("the raw transcript"))
	cu := newStubCleanup(t)
	cu.respCode = http.StatusInternalServerError

	srv, _ := newTestServer(t, asr.srv.URL, cu.srv.URL, nil)

	resp := postTranscribe(t, srv.URL+"/v1/transcribe", []byte("audio"), nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (fallback)", resp.StatusCode)
	}
	got := decodeTranscribe(t, resp)
	if got.Text != "the raw transcript" {
		t.Errorf("text = %q, want raw fallback", got.Text)
	}
	if got.CleanupApplied {
		t.Error("cleanup_applied should be false on fallback")
	}
	if got.Warning == "" {
		t.Error("warning expected on cleanup fallback")
	}
}

func TestTranscribeCleanupTimeoutFallsBackToRaw(t *testing.T) {
	asr := newStubASR(t, stubASROK("raw text here"))
	cu := newStubCleanup(t)
	cu.handlerFn = func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // client timeout is 100ms; we never answer in time
	}
	srv, _ := newTestServer(t, asr.srv.URL, cu.srv.URL, func(c *Config) {
		c.UpstreamTimeout = 100 * time.Millisecond
	})

	resp := postTranscribe(t, srv.URL+"/v1/transcribe", []byte("audio"), nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (cleanup timeout must degrade)", resp.StatusCode)
	}
	got := decodeTranscribe(t, resp)
	if got.Text != "raw text here" || got.CleanupApplied || got.Warning == "" {
		t.Errorf("got %+v", got)
	}
}

func TestTranscribeASRTimeoutReturns504(t *testing.T) {
	asr := newStubASR(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		stubASROK("too late")(w, r)
	})
	cu := newStubCleanup(t)
	srv, _ := newTestServer(t, asr.srv.URL, cu.srv.URL, func(c *Config) {
		c.UpstreamTimeout = 100 * time.Millisecond
	})

	resp := postTranscribe(t, srv.URL+"/v1/transcribe", []byte("audio"), nil, nil)
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", resp.StatusCode)
	}
	if e := decodeError(t, resp); e.Error.Type != "upstream_timeout" {
		t.Errorf("error type = %q", e.Error.Type)
	}
}

func TestTranscribeMissingFile(t *testing.T) {
	asr := newStubASR(t, stubASROK("x"))
	cu := newStubCleanup(t)
	srv, _ := newTestServer(t, asr.srv.URL, cu.srv.URL, nil)

	// Multipart with only text fields — no file part.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("language", "en")
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/transcribe", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if asr.calls.Load() != 0 {
		t.Error("ASR must not be called for request without file")
	}
}

func TestTranscribeEmptyFile(t *testing.T) {
	asr := newStubASR(t, stubASROK("x"))
	cu := newStubCleanup(t)
	srv, _ := newTestServer(t, asr.srv.URL, cu.srv.URL, nil)

	resp := postTranscribe(t, srv.URL+"/v1/transcribe", []byte{}, map[string]string{"file": ""}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for zero-byte upload", resp.StatusCode)
	}
}

func TestTranscribeNotMultipart(t *testing.T) {
	asr := newStubASR(t, stubASROK("x"))
	cu := newStubCleanup(t)
	srv, _ := newTestServer(t, asr.srv.URL, cu.srv.URL, nil)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/transcribe", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestTranscribeTooLarge(t *testing.T) {
	asr := newStubASR(t, stubASROK("x"))
	cu := newStubCleanup(t)
	srv, _ := newTestServer(t, asr.srv.URL, cu.srv.URL, func(c *Config) {
		c.MaxAudioBytes = 1024
	})

	resp := postTranscribe(t, srv.URL+"/v1/transcribe", bytes.Repeat([]byte("z"), 4096), nil, nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
	if asr.calls.Load() != 0 {
		t.Error("oversized upload must not reach ASR")
	}
}

func TestRouting(t *testing.T) {
	asr := newStubASR(t, stubASROK("x"))
	cu := newStubCleanup(t)
	srv, _ := newTestServer(t, asr.srv.URL, cu.srv.URL, nil)

	// GET on the transcribe route → 405 with Allow header.
	resp, err := http.Get(srv.URL + "/v1/transcribe")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /v1/transcribe status = %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); !strings.Contains(allow, "POST") {
		t.Errorf("Allow header = %q", allow)
	}

	// Unknown path → 404.
	resp2, err := http.Get(srv.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /nope status = %d, want 404", resp2.StatusCode)
	}
}

func TestAuth(t *testing.T) {
	asr := newStubASR(t, stubASROK("hello there"))
	cu := newStubCleanup(t)
	srv, _ := newTestServer(t, asr.srv.URL, cu.srv.URL, func(c *Config) {
		c.APIToken = "sekrit-token"
	})

	t.Run("missing", func(t *testing.T) {
		resp := postTranscribe(t, srv.URL+"/v1/transcribe", []byte("a"), nil, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})
	t.Run("wrong", func(t *testing.T) {
		resp := postTranscribe(t, srv.URL+"/v1/transcribe", []byte("a"), nil,
			map[string]string{"Authorization": "Bearer wrong"})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})
	t.Run("ok", func(t *testing.T) {
		resp := postTranscribe(t, srv.URL+"/v1/transcribe", []byte("a"), nil,
			map[string]string{"Authorization": "Bearer sekrit-token"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
	})
	t.Run("healthz open", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("healthz with auth enabled: status = %d, want 200", resp.StatusCode)
		}
	})
}

func TestCleanupMaxTokens(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{0, 64},
		{10, 64},
		{200, 100},
		{10000, 2048},
	}
	for _, c := range cases {
		if got := cleanupMaxTokens(strings.Repeat("x", c.in)); got != c.want {
			t.Errorf("cleanupMaxTokens(len=%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
