package main

import (
	"os"
	"strconv"
	"time"
)

// Config holds all runtime settings for the ASR API. Everything comes from
// environment variables so the same binary runs in compose and in tests.
type Config struct {
	// ListenAddr is the HTTP listen address, e.g. ":8080".
	ListenAddr string
	// ASRBaseURL / CleanupBaseURL are OpenAI-compatible roots ending in /v1.
	ASRBaseURL     string
	CleanupBaseURL string
	KokoroBaseURL  string
	// Model names sent upstream (match --served-model-name in compose).
	ASRModelName     string
	CleanupModelName string
	KokoroModelName  string
	// KokoroDefaultVoice is the voice used when /v1/speak omits one.
	KokoroDefaultVoice string
	// CleanupStyling is the s1-mini control-line register: casual,
	// semi-casual, semi-formal, or formal.
	CleanupStyling string
	// CleanupPromptFile is the path to the cleanup system prompt text file.
	// It is the single source of truth shared with promptfoo (evals/).
	CleanupPromptFile string
	// APIToken, when non-empty, requires "Authorization: Bearer <token>"
	// on every endpoint except /healthz.
	APIToken string
	// MaxAudioBytes caps the accepted upload size.
	MaxAudioBytes int64
	// MaxSpeakChars caps the length of text accepted by /v1/speak.
	MaxSpeakChars int
	// UpstreamTimeout bounds each individual upstream call (per transcription
	// leg, not per request).
	UpstreamTimeout time.Duration
}

// LoadConfig reads configuration from the environment with sane defaults.
func LoadConfig() Config {
	return Config{
		ListenAddr:         envStr("LISTEN_ADDR", ":8080"),
		ASRBaseURL:         envStr("ASR_BASE_URL", "http://asr-model:8000/v1"),
		CleanupBaseURL:     envStr("CLEANUP_BASE_URL", "http://cleanup-model:8000/v1"),
		KokoroBaseURL:      envStr("KOKORO_BASE_URL", "http://kokoro-model:8000/v1"),
		ASRModelName:       envStr("ASR_MODEL_NAME", "qwen3-asr"),
		CleanupModelName:   envStr("CLEANUP_MODEL_NAME", "s1-mini"),
		KokoroModelName:    envStr("KOKORO_MODEL_NAME", "kokoro"),
		KokoroDefaultVoice: envStr("KOKORO_DEFAULT_VOICE", "af_heart"),
		CleanupStyling:     envStr("CLEANUP_STYLING", "semi-formal"),
		CleanupPromptFile:  envStr("CLEANUP_PROMPT_FILE", "prompts/cleanup-system.txt"),
		APIToken:           os.Getenv("ASR_API_TOKEN"),
		MaxAudioBytes:      envInt64("MAX_AUDIO_BYTES", 25<<20),
		MaxSpeakChars:      envInt("MAX_SPEAK_CHARS", 10_000),
		UpstreamTimeout:    time.Duration(envInt("UPSTREAM_TIMEOUT_SECONDS", 120)) * time.Second,
	}
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}
