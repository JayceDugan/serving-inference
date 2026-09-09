// Command asr-api is the client-facing HTTP surface of the local ASR stack.
//
// It accepts audio uploads, transcribes them with Qwen3-ASR (vLLM), optionally
// normalizes the raw transcript with the s1-mini text-normalizer model (vLLM
// chat completions), and returns JSON. This is the only service the MacBook client
// should talk to; the model services stay private on the compose network.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	cfg     Config
	asr     *ASRClient
	cleanup *CleanupClient
	tts     *TTSClient
}

func NewServer(cfg Config, asr *ASRClient, cleanup *CleanupClient, tts *TTSClient) *Server {
	return &Server{cfg: cfg, asr: asr, cleanup: cleanup, tts: tts}
}

// Handler returns the fully wired HTTP handler (routes + auth middleware).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /v1/transcribe", s.handleTranscribe)
	mux.HandleFunc("POST /v1/speak", s.handleSpeak)
	return withAuth(s.cfg.APIToken, mux)
}

// --- response shapes -------------------------------------------------------

// TranscribeResponse is the 200 body of POST /v1/transcribe.
type TranscribeResponse struct {
	Text           string            `json:"text"` // final (cleaned) text
	RawText        string            `json:"raw_text"`
	CleanupApplied bool              `json:"cleanup_applied"`
	Warning        string            `json:"warning,omitempty"` // set when cleanup fell back to raw text
	Timings        TranscribeTimings `json:"timings_ms"`
}

type TranscribeTimings struct {
	ASR     int64 `json:"asr"`
	Cleanup int64 `json:"cleanup"`
	Total   int64 `json:"total"`
}

type errorBody struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func writeJSON(h http.ResponseWriter, status int, v any) {
	h.Header().Set("Content-Type", "application/json")
	h.WriteHeader(status)
	if err := json.NewEncoder(h).Encode(v); err != nil {
		log.Printf("response encode error: %v", err)
	}
}

func writeError(h http.ResponseWriter, status int, typ, msg string) {
	var b errorBody
	b.Error.Message = msg
	b.Error.Type = typ
	writeJSON(h, status, b)
}

// upstreamHTTPStatus maps an upstream failure to the client-facing status.
func upstreamHTTPStatus(err error) (int, string) {
	var ue *UpstreamError
	if errors.As(err, &ue) && ue.Deadline {
		return http.StatusGatewayTimeout, "upstream_timeout"
	}
	return http.StatusBadGateway, "upstream_error"
}

// --- handlers --------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleTranscribe is the single client endpoint:
//
//	POST /v1/transcribe  multipart/form-data
//	  file      (required) audio file; any format vLLM/Qwen3-ASR accepts
//	  language  (optional) language hint, e.g. "en"
//	  prompt    (optional) context/initial-prompt hint passed to the ASR model
//	  cleanup   (optional, default true) disable with false/0/off/no
//
// Returns 200 TranscribeResponse. Cleanup failures degrade to the raw
// transcript with cleanup_applied=false and a warning; ASR failures are
// reported as 502/504.
func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Bound the whole request body: audio cap plus multipart framing slack.
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxAudioBytes+(1<<20))

	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large",
				"audio upload exceeds the size limit")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request",
			"expected multipart/form-data with a 'file' field: "+err.Error())
		return
	}

	file, hdr, err := r.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.Is(err, http.ErrMissingFile):
			writeError(w, http.StatusBadRequest, "invalid_request", "missing 'file' form field")
		case errors.As(err, &maxErr):
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large",
				"audio upload exceeds the size limit")
		default:
			writeError(w, http.StatusBadRequest, "invalid_request", "cannot read 'file' field: "+err.Error())
		}
		return
	}
	defer file.Close()

	// Enforce the cap on the actual audio bytes (framing is bounded
	// separately by MaxBytesReader above). +1 lets us detect overflow.
	audio, err := io.ReadAll(io.LimitReader(file, s.cfg.MaxAudioBytes+1))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large",
				"audio upload exceeds the size limit")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request", "reading audio: "+err.Error())
		return
	}
	if int64(len(audio)) > s.cfg.MaxAudioBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large",
			"audio upload exceeds the size limit")
		return
	}
	if len(audio) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "empty audio upload")
		return
	}

	filename := hdr.Filename
	if filename == "" {
		filename = "audio.wav"
	}
	language := strings.TrimSpace(r.FormValue("language"))
	prompt := strings.TrimSpace(r.FormValue("prompt"))
	wantCleanup := parseBoolDefault(r.FormValue("cleanup"), true)

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.UpstreamTimeout)
	defer cancel()

	rawText, err := s.asr.Transcribe(ctx, audio, filename, language, prompt)
	if err != nil {
		status, typ := upstreamHTTPStatus(err)
		log.Printf("asr failure: %v", err)
		writeError(w, status, typ, err.Error())
		return
	}
	asrDone := time.Since(start)

	resp := TranscribeResponse{
		RawText: rawText,
		Text:    rawText,
		Timings: TranscribeTimings{ASR: asrDone.Milliseconds()},
	}

	if wantCleanup && strings.TrimSpace(rawText) != "" {
		cleaned, err := s.cleanup.Cleanup(ctx, rawText)
		if err != nil {
			// Dictation must survive cleanup outages: fall back to raw text.
			log.Printf("cleanup failure (falling back to raw transcript): %v", err)
			resp.Warning = "cleanup unavailable; returned raw transcript"
		} else {
			resp.Text = cleaned
			resp.CleanupApplied = true
		}
		resp.Timings.Cleanup = (time.Since(start) - asrDone).Milliseconds()
	}
	resp.Timings.Total = time.Since(start).Milliseconds()

	writeJSON(w, http.StatusOK, resp)
}

// handleSpeak is the text-to-speech client endpoint:
//
//	POST /v1/speak  application/json
//	  { "text": "required", "voice": "af_heart (optional)", "speed": 1.0 (optional) }
//
// Returns 200 with the synthesized audio (audio/wav, 24 kHz mono PCM16) and
// an X-Process-Time-Ms header. Upstream failures are reported as 502/504.
func (s *Server) handleSpeak(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// The body is tiny JSON; the text cap below bounds any realistic size.
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req struct {
		Text  string   `json:"text"`
		Voice string   `json:"voice"`
		Speed *float64 `json:"speed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request",
			"expected JSON body with a 'text' field: "+err.Error())
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "'text' must be non-empty")
		return
	}
	if len(text) > s.cfg.MaxSpeakChars {
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_long",
			"text exceeds the length limit")
		return
	}
	voice := strings.TrimSpace(req.Voice)
	if voice == "" {
		voice = s.cfg.KokoroDefaultVoice
	}
	speed := 1.0
	if req.Speed != nil {
		speed = *req.Speed
	}
	if speed < 0.25 || speed > 4 {
		writeError(w, http.StatusBadRequest, "invalid_request", "'speed' must be between 0.25 and 4")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.UpstreamTimeout)
	defer cancel()

	audio, err := s.tts.Speak(ctx, text, voice, speed)
	if err != nil {
		status, typ := upstreamHTTPStatus(err)
		log.Printf("kokoro failure: %v", err)
		writeError(w, status, typ, err.Error())
		return
	}

	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("X-Process-Time-Ms", strconv.FormatInt(time.Since(start).Milliseconds(), 10))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(audio); err != nil {
		log.Printf("writing speak response: %v", err)
	}
}

// parseBoolDefault interprets form booleans: empty/missing → def;
// false-ish: false/0/off/no; true-ish: true/1/on/yes (case-insensitive);
// anything else falls back to strconv.ParseBool, then def.
func parseBoolDefault(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return def
	case "false", "0", "off", "no":
		return false
	case "true", "1", "on", "yes":
		return true
	}
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	return def
}

// --- auth ------------------------------------------------------------------

// withAuth enforces "Authorization: Bearer <token>" when token is non-empty.
// /healthz stays open for container healthchecks.
func withAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		got := []byte(bearerToken(r))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// --- main ------------------------------------------------------------------

func main() {
	cfg := LoadConfig()

	prompt, err := os.ReadFile(cfg.CleanupPromptFile)
	if err != nil {
		log.Fatalf("loading cleanup prompt %s: %v", cfg.CleanupPromptFile, err)
	}
	cleanupPrompt := strings.TrimSpace(string(prompt))
	if cleanupPrompt == "" {
		log.Fatalf("cleanup prompt file %s is empty", cfg.CleanupPromptFile)
	}

	httpClient := &http.Client{}
	asr := &ASRClient{BaseURL: cfg.ASRBaseURL, Model: cfg.ASRModelName, HTTP: httpClient}
	cleanup := &CleanupClient{BaseURL: cfg.CleanupBaseURL, Model: cfg.CleanupModelName,
		SystemPrompt: cleanupPrompt, Styling: cfg.CleanupStyling, HTTP: httpClient}
	tts := &TTSClient{BaseURL: cfg.KokoroBaseURL, Model: cfg.KokoroModelName, HTTP: httpClient}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           NewServer(cfg, asr, cleanup, tts).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("asr-api listening on %s (asr=%s model=%s, cleanup=%s model=%s styling=%s prompt=%s, kokoro=%s voice=%s, auth=%v, max_upload=%d bytes)",
		cfg.ListenAddr, cfg.ASRBaseURL, cfg.ASRModelName,
		cfg.CleanupBaseURL, cfg.CleanupModelName, cfg.CleanupStyling, cfg.CleanupPromptFile,
		cfg.KokoroBaseURL, cfg.KokoroDefaultVoice, cfg.APIToken != "", cfg.MaxAudioBytes)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
}
