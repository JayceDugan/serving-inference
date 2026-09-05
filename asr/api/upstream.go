package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

// UpstreamError reports a failure from one of the model services. Status is
// the upstream HTTP status when the request reached it (0 for transport
// failures); Deadline tells the handler to answer 504 instead of 502.
type UpstreamError struct {
	Service  string // "asr" or "cleanup"
	Status   int    // upstream HTTP status, 0 if no response
	Deadline bool   // request timed out
	Detail   string // short sanitized snippet of the upstream body
	err      error
}

func (e *UpstreamError) Error() string {
	if e.Deadline {
		return fmt.Sprintf("%s upstream timed out", e.Service)
	}
	if e.Status != 0 {
		return fmt.Sprintf("%s upstream returned %d: %s", e.Service, e.Status, e.Detail)
	}
	return fmt.Sprintf("%s upstream request failed: %v", e.Service, e.err)
}

func (e *UpstreamError) Unwrap() error { return e.err }

// newUpstreamError classifies a transport-level error from an upstream.
func newUpstreamError(service string, err error) *UpstreamError {
	e := &UpstreamError{Service: service, err: err}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		e.Deadline = true
	}
	return e
}

// bodySnippet keeps error messages bounded and single-line.
func bodySnippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// ASRClient talks to the Qwen3-ASR vLLM service via the OpenAI transcription
// endpoint (POST {base}/audio/transcriptions).
type ASRClient struct {
	BaseURL string // e.g. http://asr-model:8000/v1
	Model   string
	HTTP    *http.Client
}

// Transcribe sends raw audio bytes and returns the raw transcript text.
// language and prompt are optional hints; empty strings are omitted.
func (c *ASRClient) Transcribe(ctx context.Context, audio []byte, filename, language, prompt string) (string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	partHeader := make(map[string][]string)
	partHeader["Content-Disposition"] = []string{
		fmt.Sprintf(`form-data; name="file"; filename=%q`, filename),
	}
	part, err := mw.CreatePart(partHeader)
	if err != nil {
		return "", fmt.Errorf("building multipart: %w", err)
	}
	if _, err := part.Write(audio); err != nil {
		return "", fmt.Errorf("building multipart: %w", err)
	}
	if err := mw.WriteField("model", c.Model); err != nil {
		return "", err
	}
	if language != "" {
		if err := mw.WriteField("language", language); err != nil {
			return "", err
		}
	}
	if prompt != "" {
		if err := mw.WriteField("prompt", prompt); err != nil {
			return "", err
		}
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", newUpstreamError("asr", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", newUpstreamError("asr", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", &UpstreamError{Service: "asr", Status: resp.StatusCode, Detail: bodySnippet(raw)}
	}

	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", &UpstreamError{Service: "asr", Status: resp.StatusCode,
			Detail: "invalid JSON from transcription endpoint: " + bodySnippet(raw)}
	}
	return strings.TrimSpace(payload.Text), nil
}

// CleanupClient talks to the cleanup instruct model via OpenAI chat
// completions (POST {base}/chat/completions). SystemPrompt is loaded from a
// text file at startup (see Config.CleanupPromptFile) — the same file
// promptfoo reads, so there is one source of truth for both.
type CleanupClient struct {
	BaseURL      string
	Model        string
	SystemPrompt string
	HTTP         *http.Client
}

// Cleanup returns the polished version of a raw ASR transcript.
func (c *CleanupClient) Cleanup(ctx context.Context, raw string) (string, error) {
	payload := map[string]any{
		"model":    c.Model,
		"messages": c.messages(raw),
		// Deterministic edits; size the output budget from the input.
		"temperature": 0,
		"max_tokens":  cleanupMaxTokens(raw),
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", newUpstreamError("cleanup", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", newUpstreamError("cleanup", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", &UpstreamError{Service: "cleanup", Status: resp.StatusCode, Detail: bodySnippet(body)}
	}

	var chat struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &chat); err != nil {
		return "", &UpstreamError{Service: "cleanup", Status: resp.StatusCode,
			Detail: "invalid JSON from chat completions: " + bodySnippet(body)}
	}
	if len(chat.Choices) == 0 {
		return "", &UpstreamError{Service: "cleanup", Status: resp.StatusCode, Detail: "no choices in response"}
	}
	out := strings.TrimSpace(chat.Choices[0].Message.Content)
	if out == "" {
		return "", &UpstreamError{Service: "cleanup", Status: resp.StatusCode, Detail: "empty cleanup output"}
	}
	return out, nil
}

// messages builds the two-message chat body. Exported-ish via package tests
// so prompt content is verifiable.
func (c *CleanupClient) messages(raw string) []map[string]string {
	return []map[string]string{
		{"role": "system", "content": c.SystemPrompt},
		{"role": "user", "content": raw},
	}
}

// cleanupMaxTokens scales the generation budget with input size (roughly 3
// characters per token, doubled for safety), bounded to [64, 2048].
func cleanupMaxTokens(raw string) int {
	n := len([]rune(raw)) / 2
	if n < 64 {
		return 64
	}
	if n > 2048 {
		return 2048
	}
	return n
}
