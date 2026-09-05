# Local ASR Service (Whisprflow replacement)

Self-hosted speech-to-text on the war rig. The rig transcribes over the LAN;
the MacBook client owns the OS surface (menu bar, hotkey, text injection).
Non-streaming only — one audio upload in, one cleaned transcript out.

```
┌─────────────┐  POST /v1/transcribe   ┌─────────────────── war rig ──────────────────────┐
│  MacBook    │ ────── multipart ────▶ │  asr-api :8090 (Go)                              │
│  menu bar + │                        │      │                                           │
│  hotkey     │ ◀──── JSON ─────────── │      ├─▶ asr-model:8000      Qwen3-ASR-1.7B      │
└─────────────┘                        │      └─▶ cleanup-model:8000  superwhisper/s1-mini│
                                       │                (0.6B text normalizer)            │
                                       │   all three pinned to the RTX 5080 (device 1)    │
                                       └──────────────────────────────────────────────────┘
```

The MacBook talks **only** to `asr-api`. The two vLLM services have no
host-facing ports beyond loopback debug bindings.

## Quickstart

Prereqs (already satisfied on the war rig): Docker + Compose v2+, NVIDIA
Container Toolkit, weights cached under `~/.cache/huggingface`
(`hf download Qwen/Qwen3-ASR-1.7B && hf download Qwen/Qwen3-4B-Instruct-2507-FP8`).

```bash
cp asr/.env.example asr/.env      # then set ASR_API_TOKEN (see Auth)
make asr-up                       # = cd asr && docker compose --profile asr up -d --build
curl http://localhost:8090/healthz
curl -F "file=@asr/api/testdata/jfk.wav" http://localhost:8090/v1/transcribe
```

Cold start is ~6 minutes (the two vLLM engines load **serially**, by design —
see GPU bring-up). `docker compose --profile asr logs -f` or `make asr-logs`.

Stop with `make asr-down`.

## Compose layout & profile isolation

Everything lives in `asr/docker-compose.yml` as its own compose project
(`ai-lab-asr`, network `ai-lab-asr_net`) — deliberately separate from the root
`docker-compose.yml`:

- All three services carry `profiles: ["asr"]`; a bare `docker compose up`
  in `asr/` resolves to an **empty** service list. Nothing starts without the
  profile.
- The root project (`openwebui`, `stealthy-browser`, profiles
  `unsloth`/`vllm`/`litellm`) contains no ASR services, so no existing
  workload can start this stack as a side effect, and `--profile asr` cannot
  disturb them. Profile name `asr` does not collide with anything on the box.

Verified via:

```bash
docker compose config --services                 # root: openwebui, stealthy-browser (no asr)
cd asr && docker compose config --services       # empty — profile required
cd asr && docker compose --profile asr config --services
# → asr-model, cleanup-model, asr-api
```

## GPU targeting (RTX 5080 only)

The 5090 (host device 0) runs the other llama.cpp/vLLM workloads and must stay
untouched. Both GPU consumers pin with:

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

### asr-api — Go facade (the only client-facing surface)

Stdlib-only Go 1.25 service (`api/`), multi-stage Dockerfile → alpine, runs
non-root. Config via env (see `.env.example` and `api/config.go`):

| Env var | Default | Meaning |
|---|---|---|
| `LISTEN_ADDR` | `:8080` (in container) | HTTP bind |
| `ASR_BASE_URL` / `ASR_MODEL_NAME` | `http://asr-model:8000/v1` / `qwen3-asr` | transcription upstream |
| `CLEANUP_BASE_URL` / `CLEANUP_MODEL_NAME` | `http://cleanup-model:8000/v1` / `s1-mini` | cleanup upstream (text normalizer) |
| `CLEANUP_STYLING` | `semi-formal` | s1-mini register: casual, semi-casual, semi-formal, formal |
| `ASR_API_TOKEN` | empty | bearer token; empty = no auth (LAN only) |
| `MAX_AUDIO_BYTES` | `26214400` (25 MiB) | upload cap, enforced on the file part itself |
| `UPSTREAM_TIMEOUT_SECONDS` | `120` | per-leg upstream timeout |

## VRAM budget (measured, both engines loaded)

```
RTX 5080 total:                          16303 MiB
asr-model     EngineCore (util 0.42):     6234 MiB   KV cache 12,784 tokens
cleanup-model EngineCore (util 0.20):     3246 MiB   KV cache 13,968 tokens
other processes:                            ~50 MiB
─────────────────────────────────────────────────────
device usage                              9528 MiB → headroom ≈ 6.7 GiB
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

### `GET /healthz`

`{"status":"ok"}` — always open (no auth) for container healthchecks. Use it
as client readiness signal; it only answers once `asr-api` is up, which
compose gates on both engines being healthy.

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
echo "ASR_API_TOKEN=$(openssl rand -base64 32)" >> asr/.env
docker compose --profile asr up -d   # recreates asr-api only
```

Clients send `Authorization: Bearer <token>`. `/healthz` stays open. No TLS —
put Caddy/Traefik in front if the LAN isn't trusted.

## Testing

```bash
make asr-test                     # or: cd asr/api && go test -race ./...
```

16 test functions (subtests included) over `httptest` stubs of both upstreams:
request forwarding fidelity (exact audio bytes, multipart fields, model names,
chat-body prompt content), cleanup on/off parsing, ASR error → 502, malformed
upstream JSON → 502, cleanup failure/timeout → raw fallback with warning, ASR
timeout → 504, missing/empty/non-multipart/oversized uploads, routing 404/405,
bearer-auth matrix, `max_tokens` sizing. No live GPU needed for tests.

## Troubleshooting (all of these were actually hit)

| Symptom | Cause / fix |
|---|---|
| 400 `Invalid or unsupported audio file` from vLLM on any format | Base image lacks `[audio]` extra → rebuild the overlay (`make asr-up` already builds) and recreate; check `docker exec asr-model python3 -c "import soundfile"`. |
| `AssertionError: Error in memory profiling ... release GPU memory while vLLM is profiling` | Two engines initializing concurrently on one GPU. Keep the `depends_on: service_healthy` chain on `cleanup-model`. |
| Warning: `you should provide the model as a positional argument` | vLLM 0.28 deprecation; compose already uses positional form. |
| `rope_parameters` FutureWarning in asr-model logs | Harmless transformers version noise; transcription unaffected. |
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
