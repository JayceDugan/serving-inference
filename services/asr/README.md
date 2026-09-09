# Local ASR + TTS Service (Whisprflow replacement)

Self-hosted speech on the war rig. The rig transcribes over the LAN and can
speak text back; the MacBook client owns the OS surface (menu bar, hotkey,
text injection). Non-streaming only — one audio upload in, one cleaned
transcript out; one text post in, one WAV out.
```
┌─────────────┐  POST /v1/transcribe   ┌─────────────────── war rig ──────────────────────┐
│  MacBook    │ ────── multipart ────▶ │  asr-api :8090 (Go)                              │
│  menu bar + │                        │      │                                           │
│  hotkey     │ ◀──── JSON ─────────── │      ├─▶ asr-model:8000      Qwen3-ASR-1.7B      │
└─────────────┘                        │      ├─▶ cleanup-model:8000  superwhisper/s1-mini│
                                       │      └─▶ kokoro-model:8000   Kokoro-82M TTS      │
                                       │   all GPU consumers pinned to the RTX 5080 (dev. 1)│
                                       └──────────────────────────────────────────────────┘
```

The MacBook talks **only** to `asr-api`. The two vLLM services have no
host-facing ports beyond loopback debug bindings.

## Quickstart

Prereqs (already satisfied on the war rig): Docker + Compose v2+, NVIDIA
Container Toolkit, weights cached under `~/.cache/huggingface`
(`hf download Qwen/Qwen3-ASR-1.7B && hf download Qwen/Qwen3-4B-Instruct-2507-FP8`).

```bash
# ASR keys live in the root .env (see .env.example); set ASR_API_TOKEN there (see Auth)
make asr-up                       # = docker compose up -d --build asr-model cleanup-model kokoro-model asr-api
curl http://localhost:8090/healthz
curl -F "file=@services/asr/api/testdata/jfk.wav" http://localhost:8090/v1/transcribe
curl -X POST http://localhost:8090/v1/speak -H 'Content-Type: application/json' \
     -d '{"text":"The war rig says hello.","voice":"af_heart"}' -o hello.wav
```

Cold start is ~6 minutes (the two vLLM engines load **serially**, by design —
see GPU bring-up). Follow with `make asr-logs`.

Stop with `make asr-down`.

## Compose layout

All services live in the root `docker-compose.yml` — a single project
(`ai-lab`). The ASR services are **not** profile-gated: they come up with any
`docker compose up`, alongside Open WebUI and the embedding service. Only
`unsloth` (profile `unsloth`) and `stealthy-browser` (profile
`agent-browser`) stay behind profiles. The dedicated network `asr-net`
(Docker name `ai-lab-asr_net`) keeps ASR off the shared
`ai-lab-inference-net`.

Verified via:

```bash
docker compose config --services
# → openwebui, embeddings_model, asr-model, cleanup-model, kokoro-model, asr-api
docker compose --profile agent-browser config --services
# adds stealthy-browser (unsloth is gated by profile unsloth)
```

## GPU targeting (RTX 5080 only)

The 5090 (host device 0) runs the other llama.cpp/vLLM workloads and must stay
untouched. All three GPU consumers pin with:

```yaml
deploy:
  resources:
    reservations:
      devices:
        - driver: nvidia
          device_ids: ["${ASR_GPU_ID:-1}"]   # host index; 1 = RTX 5080
          capabilities: [gpu]
```

Notes:

- `device_ids` uses the **host** enumeration (`nvidia-smi -L`). Inside each
  container the pinned GPU is renumbered to `cuda:0` — do **not** also set
  `CUDA_VISIBLE_DEVICES`; that would hide the reserved device.
- Verify placement (never assume):

  ```bash
  nvidia-smi --query-compute-apps=gpu_name,pid,process_name,used_memory --format=csv
  # both VLLM::EngineCore rows must read "NVIDIA GeForce RTX 5080"
  ```

## Services

### asr-model — Qwen3-ASR-1.7B (vLLM)

- Image: `ai-lab-asr-vllm:latest`, built from `model-image/Dockerfile` — a
  thin overlay on `vllm/vllm-openai:latest` that installs the `[audio]` extra
  (`av scipy soundfile soxr mistral_common[audio]`) pinned to the base image's
  vLLM version. **The stock image ships without it and every upload fails with
  HTTP 400 "Invalid or unsupported audio file."** If you change `VLLM_IMAGE`,
  rebuild (`--build`).
- Launch (model as positional arg — `--model` is deprecated in vLLM 0.28):
  `Qwen/Qwen3-ASR-1.7B --served-model-name qwen3-asr --gpu-memory-utilization
  0.42 --max-model-len 8192 --max-num-seqs 8`
- Exposes OpenAI `/v1/audio/transcriptions` (what asr-api uses) and
  `/v1/chat/completions` with `audio_url`.
- Loopback debug port: `127.0.0.1:8010`.

### cleanup-model — superwhisper/s1-mini (vLLM)

A 0.6B text normalizer fine-tuned from Qwen3-0.6B (Apache 2.0 + naming
clause): takes a raw ASR transcript and rewrites it as clean written text —
fillers removed, false starts resolved to the landed value, punctuation and
capitalization applied, spoken numbers/dates/currency/emails rendered in
written form. English only; ~1.2 GB bf16 weights fit easily in a 0.2
utilization budget on the 5080 (0.15 flaps vLLM's 8192-token KV-cache check
under profiling variance).

- Same overlay image; flags: `--served-model-name s1-mini
  --default-chat-template-kwargs '{"enable_thinking": false}'
  --gpu-memory-utilization 0.2 --max-model-len 8192 --max-num-seqs 2`.
- Not a chat model — it follows only its trained input shape: the exact
  system prompt plus a `[Styling: …] [Structure: …] [Context: …]` control
  line above the transcript. asr-api builds that shape; `CLEANUP_STYLING`
  selects the register (default `semi-formal`). Thinking must stay off or the
  model emits an empty think block and stops.
- Filler/noise-only input legitimately yields an empty string; asr-api treats
  that as a valid cleaned result, not a failure.
- Loopback debug port: `127.0.0.1:8011`.
- `depends_on: asr-model: service_healthy` — vLLM asserts GPU free memory is
  stable during its memory-profiling step; two engines initializing
  concurrently on one device trip `AssertionError: Error in memory profiling`.
  Serial bring-up avoids this deterministically (also on host reboot, since
  dockerd honors compose start order for `restart: unless-stopped`).

### kokoro-model — hexgrad/Kokoro-82M (FastAPI)

The TTS leg. 82M-parameter open-weight model (~325 MB fp16 weights, ~0.5 GiB
VRAM warm) served behind an OpenAI-style `POST /v1/audio/speech` by a small
FastAPI app (`kokoro-image/server.py`, image `ai-lab-kokoro:latest`).
Kokoro generates 24 kHz mono float32; the server writes it as WAV, so the
response needs no client-side decoding beyond any audio framework.

- Language: one pipeline per process, selected by `KOKORO_LANG_CODE`
  (`a` = American English default, `b` = British). Voice codes must match the
  prefix (`af_*`, `am_*`, `bf_*`, `bm_*`); a mismatched voice is rejected with
  a 400 naming the available voices.
- Voices: the full hexgrad set (e.g. `af_heart`, `af_nicole`, `am_adam`,
  `bf_emma`). `KOKORO_DEFAULT_VOICE` picks the fallback for requests that omit
  `voice`.
- Speed: `speed` ∈ [0.5, 2.0], default 1.0 (pass-through to Kokoro's
  generator).
- Loopback debug port: `127.0.0.1:8012` (`KOKORO_MODEL_DEBUG_PORT`).
- First start downloads ~0.4 GB of weights + voice packs from HF into the
  shared host cache (the healthcheck allows up to 10 min for it); afterwards
  startup is ~30 s (torch import + model load).
- `depends_on: asr-model: service_healthy` — same serial bring-up rule as
  cleanup-model, so its weight load never races vLLM's memory-profiling
  window on the shared card.

### asr-api — Go facade (the only client-facing surface)

Stdlib-only Go 1.25 service (`api/`), multi-stage Dockerfile → alpine, runs
non-root. Config via env (see `.env.example` and `api/config.go`):

| Env var | Default | Meaning |
|---|---|---|
| `LISTEN_ADDR` | `:8080` (in container) | HTTP bind |
| `ASR_BASE_URL` / `ASR_MODEL_NAME` | `http://asr-model:8000/v1` / `qwen3-asr` | transcription upstream |
| `CLEANUP_BASE_URL` / `CLEANUP_MODEL_NAME` | `http://cleanup-model:8000/v1` / `s1-mini` | cleanup upstream (text normalizer) |
| `KOKORO_BASE_URL` / `KOKORO_MODEL_NAME` | `http://kokoro-model:8000/v1` / `kokoro` | TTS upstream |
| `KOKORO_DEFAULT_VOICE` | `af_heart` | voice used when a speak request omits one |
| `CLEANUP_STYLING` | `semi-formal` | s1-mini register: casual, semi-casual, semi-formal, formal |
| `ASR_API_TOKEN` | empty | bearer token; empty = no auth (LAN only) |
| `MAX_AUDIO_BYTES` | `26214400` (25 MiB) | upload cap, enforced on the file part itself |
| `MAX_SPEAK_CHARS` | `10000` | text cap for /v1/speak (Kokoro handles long text via splitting) |
| `UPSTREAM_TIMEOUT_SECONDS` | `120` | per-leg upstream timeout |

## VRAM budget (measured, all three model services loaded)

```
RTX 5080 total:                          16303 MiB
asr-model     EngineCore (util 0.42):     6680 MiB   KV cache 12,784 tokens
cleanup-model EngineCore (util 0.20):     3248 MiB   KV cache 13,968 tokens
kokoro-model  FastAPI + Kokoro-82M:        644 MiB   (weights + CUDA context)
─────────────────────────────────────────────────────
device usage                             10572 MiB → headroom ≈ 5.6 GiB
```

Headroom knobs: `ASR_GPU_MEM_UTIL` / `CLEANUP_GPU_MEM_UTIL` in `.env`. KV
cache at these budgets supports ~1.6× concurrency at the full 8192-token
context per engine (cleanup is capped at 2 concurrent sequences by
`--max-num-seqs`, matching solo serial dictation) — ample for single-user
dictation; raise `CLEANUP_GPU_MEM_UTIL` if you ever see queueing on the
cleanup leg.

## API reference

### `POST /v1/transcribe`

`multipart/form-data`:

| Field | Required | Notes |
|---|---|---|
| `file` | yes | audio; verified: PCM WAV 16-bit/16 kHz mono and 24-bit/48 kHz (server resamples). MP3/FLAC/OGG/M4A accepted via soundfile/pyav in the overlay — untested from a client. |
| `language` | no | language hint passed upstream, e.g. `en` |
| `prompt` | no | context/initial-prompt hint for the ASR model |
| `cleanup` | no | default `true`; disable with `false`/`0`/`off`/`no` |

200 response:

```json
{
  "text": "And so, my fellow Americans, ask not what your country can do for you. Ask what you can do for your country.",
  "raw_text": "<same — raw Qwen3-ASR output>",
  "cleanup_applied": true,
  "warning": "only present when cleanup failed and text fell back to raw",
  "timings_ms": { "asr": 166, "cleanup": 176, "total": 343 }
}
```

Error contract (JSON `{"error":{"message","type"}}`):

| Status | type | Cause |
|---|---|---|
| 400 | `invalid_request` | missing/empty `file`, non-multipart body |
| 401 | `unauthorized` | bearer token set but missing/wrong |
| 404 / 405 | — | unknown path / wrong method (405 carries `Allow`) |
| 413 | `payload_too_large` | audio exceeds `MAX_AUDIO_BYTES` |
| 502 | `upstream_error` | ASR engine error or malformed upstream JSON |
| 504 | `upstream_timeout` | ASR leg exceeded `UPSTREAM_TIMEOUT_SECONDS` |

**Cleanup failures never fail the request**: a cleanup-engine error/timeout
returns 200 with `text == raw_text`, `cleanup_applied: false`, and a
`warning`. Dictation must survive the cleanup engine being down; only the ASR
leg is load-bearing.

### `POST /v1/speak`

`application/json`:

| Field | Required | Notes |
|---|---|---|
| `text` | yes | text to synthesize; missing/whitespace-only → 400. Hard cap `MAX_SPEAK_CHARS` (default 10,000) → 413 |
| `voice` | no | Kokoro voice code (`af_heart`, `am_adam`, `bf_emma`, …). Must match the pipeline language prefix; omitted → `KOKORO_DEFAULT_VOICE` |
| `speed` | no | float in [0.5, 2.0], default 1.0 |

file; there is no JSON wrapper. Warm generation is far faster than real time
(~30 ms for a short sentence); the first request that uses a given voice can
take a few seconds while Kokoro fetches that voice pack from HF.

Error contract (JSON `{"error":{"message","type"}}`, same shape as transcribe):

| Status | type | Cause |
|---|---|---|
| 400 | `invalid_request` | missing/empty `text`, non-JSON body, unknown voice, out-of-range `speed` |
| 413 | `payload_too_long` | `text` exceeds `MAX_SPEAK_CHARS` |
| 502 | `upstream_error` | Kokoro engine error or malformed upstream response |
| 504 | `upstream_timeout` | TTS leg exceeded `UPSTREAM_TIMEOUT_SECONDS` |

Unlike cleanup, a TTS failure **does** fail the request — there is no fallback for
speech. The transcription path is unaffected either way; the legs are independent.

Direct-to-model debugging bypasses the facade:
`curl -X POST http://127.0.0.1:8012/v1/audio/speech -H 'Content-Type: application/json' -d '{"model":"kokoro","input":"hello","voice":"af_heart"}' -o out.wav`.

### `GET /healthz`

`{"status":"ok"}` — always open (no auth) for container healthchecks. Use it
as client readiness signal; it only answers once `asr-api` is up, which
compose gates on all three model services being healthy.

### Verified timings (warm engine)

| Input | ASR | Cleanup | Total |
|---|---|---|---|
| jfk.wav (~11 s) | 166 ms | 176 ms | **343 ms** |
| sample_en.wav (~11 s, 48 kHz/24-bit) | 305 ms | 248 ms | **554 ms** |

Test audio lives in `api/testdata/`. Direct-to-model debugging bypasses the
facade: `curl -F file=@x.wav -F model=qwen3-asr http://127.0.0.1:8010/v1/audio/transcriptions`.

## Auth

`asr-api` supports a single bearer token (constant-time compare). Empty by
default = LAN-only. To enable:

```bash
echo "ASR_API_TOKEN=$(openssl rand -base64 32)" >> .env
docker compose up -d asr-api   # recreates asr-api only
```

Clients send `Authorization: Bearer <token>`. `/healthz` stays open. No TLS —
put Caddy/Traefik in front if the LAN isn't trusted.

## Testing

```bash
make asr-test                     # or: cd services/asr/api && go test -race ./...
```

23 test functions (subtests included) over `httptest` stubs of all three
upstreams: request forwarding fidelity (exact audio bytes, multipart fields,
model names, chat-body prompt content), cleanup on/off parsing, ASR error → 502,
malformed upstream JSON → 502, cleanup failure/timeout → raw fallback with
warning, ASR timeout → 504, missing/empty/non-multipart/oversized uploads, speak
forwarding fidelity (JSON fields, voice/speed defaults), speak error mapping
(400 empty text / bad speed, 413 too long, 502, 504), routing 404/405,
bearer-auth matrix, `max_tokens` sizing. No live GPU needed for tests.

## Troubleshooting (all of these were actually hit)

| Symptom | Cause / fix |
|---|---|
| 400 `Invalid or unsupported audio file` from vLLM on any format | Base image lacks `[audio]` extra → rebuild the overlay (`make asr-up` already builds) and recreate; check `docker exec asr-model python3 -c "import soundfile"`. |
| `AssertionError: Error in memory profiling ... release GPU memory while vLLM is profiling` | Two engines initializing concurrently on one GPU. Keep the `depends_on: service_healthy` chain — `cleanup-model` and `kokoro-model` both gate on `asr-model`. |
| Warning: `you should provide the model as a positional argument` | vLLM 0.28 deprecation; compose already uses positional form. |
| `rope_parameters` FutureWarning in asr-model logs | Harmless transformers version noise; transcription unaffected. |
| 400 from /v1/speak: `unknown voice ... available: af_..., am_...` | Voice prefix doesn't match the pipeline language (`KOKORO_LANG_CODE`). American voices are `af_*`/`am_*`; for British set `KOKORO_LANG_CODE=b` and use `bf_*`/`bm_*`. |
| Stack not on the 5080 after moving GPUs / re-seating | `nvidia-smi -L` → set `ASR_GPU_ID` in `.env`; recreate. |
| First request slow | Cold engine: bring-up takes ~6 min and compose blocks `asr-api` until both healthchecks pass; poll `/healthz`. |

---

## Mac client implementation notes

The war-rig side is fixed; this section is the contract for the MacBook menu
bar app (out of scope here, but everything it needs is below).

### Endpoint & auth

- Base URL: `http://<war-rig-lan-host>:8090` (port from `ASR_API_PORT`, bound
  `0.0.0.0`). Use a mDNS name or DHCP reservation so the client survives IP
  changes.
- If `ASR_API_TOKEN` is set, every request except `/healthz` needs
  `Authorization: Bearer <token>`. Store it in Keychain, not in app config.

### Audio capture settings (known-good)

Qwen3-ASR consumes 16 kHz mono; vLLM resamples anything else server-side.
Both of these were verified end-to-end through the API:

- PCM WAV, 16-bit, **16 kHz mono** — ideal: smallest upload, zero server-side
  resample cost.
- PCM WAV, 24-bit, 48 kHz stereo-source → accepted and resampled correctly.

Practical capture with `AVAudioEngine`: install a format converter on the
input node to `AVLinearPCMFormatFlag16BitInteger | .nonInterleaved?` mono
16000 Hz, write a WAV header + samples to a temp file (or in-memory `Data`).
Compressed formats (m4a/aac/opus/mp3) decode via the server's soundfile/pyav
stack and are almost certainly fine, but are **unverified from a client** —
WAV removes the variable.

Size cap: 25 MiB per upload (`MAX_AUDIO_BYTES`). That is ~13 minutes of
16 kHz/16-bit mono WAV — far beyond any realistic hotkey dictation; treat 413
as "stop recording" UX, not a retry.

### Request (Swift sketch)

```swift
var request = URLRequest(url: baseURL.appendingPathComponent("v1/transcribe"))
request.httpMethod = "POST"
request.timeoutInterval = 30
// if token set:
request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

let body = multipartBody(boundary: boundary,
                         file: ("recording.wav", wavData, "audio/wav"),
                         fields: ["cleanup": "true"]) // language/prompt optional
request.httpBody = body

URLSession.shared.dataTask(with: request) { data, response, error in ... }
```

Response decoding maps 1:1 to the JSON above. Prefer `text` for injection;
keep `raw_text` around if you offer an "as spoken" mode or want a diff view.

### Behavior the client must handle

- **Readiness**: on app launch (and before first use after idle), poll
  `GET /healthz`. A failure means the stack is down or cold (~6 min boot);
  show a distinct "server starting" state instead of an error toast. Consider
  a wake-on-LAN hook if the rig sleeps.
- **Latency budget**: warm round trip measured at **343–554 ms** for ~11 s of
  audio, so the whole hotkey→injected-text loop should land well under a
  second. If you see multi-second totals, suspect the network or a cold
  engine, not inference.
- **`cleanup_applied: false` + `warning`**: inject `text` anyway (it's the
  raw transcript) and optionally flash a subtle "uncleaned" indicator — never
  block pasting on cleanup being down.
- **Error mapping for UX**:
  - `401` → token mismatch; prompt to re-enter, don't retry blindly.
  - `413` → recording too long; surface as such.
  - `502`/`504` → rig-side failure; one automatic retry after ~2 s is
    reasonable (the facade is stateless and idempotent per request), then
    surface the error with the server's `error.message`.
  - Connection refused / timeout → rig offline or stack down.
- **Injection**: standard pasteboard swap + `CGEvent` ⌘V (or the private
  paste approach) — same as any dictation tool; nothing ASR-specific.
- **Timeouts**: server per-upstream timeout is 120 s; keep the client's own
  request timeout generous (≥30 s) so a long recording isn't cut off while
  still bounding failures.

### What this pass deliberately does NOT provide

- Streaming / partial transcripts (offline-only endpoint).
- Timestamps/word alignment (would need Qwen3-ForcedAligner, not deployed).
- Multi-user auth, TLS, rate limiting — single trusted LAN.
