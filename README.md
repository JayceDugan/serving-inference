# 🖥️ The War Rig — Jayce's Home AI Lab

> **Not a product. Not a framework. Nothing here is packaged for you to deploy.**
> This is my house, my desk, my power bill, and the machine that thinks in it.
> It exists so I can point at something on my wall and say *that box runs my world*.

![The devbox](docs/devbox.jpg)
<!-- TODO: photo of the rig. It deserves a good one. -->

One tower. Two Blackwell GPUs. A pile of pinned containers, speculative decoding,
and quantized weights that I keep arguing with until they behave. Everything is
local, everything is mine, and no token leaves the building unless I ask it to.

---

## ⚡ The Machine

| | |
|---|---|
| **Role** | Home AI lab / inference server / "war rig" |
| **CPU** | AMD Ryzen Threadripper PRO 7965WX — 24 cores / 48 threads, Zen 4, boost to 5.36 GHz |
| **Motherboard** | ASUS Pro WS WRX90E-SAGE SE (UEFI, 07/2025 firmware) |
| **Memory** | 256 GB (currently ~180 GB resident with models warm in page cache) |
| **GPU 0** | **NVIDIA GeForce RTX 5090 Founders Edition — 32 GB** (GB202, SM120) → *the brain* |
| **GPU 1** | **NVIDIA GeForce RTX 5080 — 16 GB** (GB203, SM120) → *the staff* |
| **Driver / CUDA** | 580.159.03 / CUDA 13.0 |
| **Storage** | Samsung SSD 990 PRO with Heatsink 2 TB · btrfs · 1.82 TiB |
| **Swap** | 8 GiB zram (because swapping to NVMe is for people who give up) |
| **Network** | Intel X710 10GBASE-T + Tailscale for "from anywhere that isn't the couch" |
| **OS** | Fedora Linux 42 (Workstation), kernel 6.19.14, GNOME/Wayland, Docker + NVIDIA Container Toolkit |

**Division of labour, enforced by `device_ids` and a healthy fear of OOM:**

```
┌──────────────── RTX 5090 · 32 GB ────────────────┐   ┌──────── RTX 5080 · 16 GB ────────┐
│  Unsloth Studio (:8888)                          │   │  asr-model      Qwen3-ASR-1.7B    │
│   └─ llama-server · Qwen3.8 Flash Next Q8        │   │  cleanup-model  Qwen3-4B-Instr FP8│
│      30 GB weights · 180K ctx · MTP spec decode  │   │  (2 vLLM engines, ~12.8 GiB total)│
│  NVFP4 Qwen3.8-27B experiments (vLLM/SGLang)     │   │  Never touches the 5090. Ever.    │
└──────────────────────────────────────────────────┘   └───────────────────────────────────┘
```

The rule: **nothing in the ASR/embeddings path may reserve a byte of the 5090.**
The 5090 is for big thinking; the 5080 runs the always-on help desk.

---

## 🧠 What's Served Right Now

### Unsloth Studio — `:8888` (host-native, GPU 0)

My front door for daily driving. Studio sits on `0.0.0.0:8888`, and underneath it
llama.cpp does the actual work on a pinned build in `~/.unsloth/llama.cpp`.

| Model | Quant | Notes |
|---|---|---|
| **Qwen3.8 Next Flash** | Q8_K_XL (live load: 6-part Q8 shard set) | The house model. Vision via `mmproj-F16.gguf`, `--flash-attn on`, unified KV, **MTP speculative decoding on by default** (`--spec-default`), thinking mode enabled and preserved across turns. |
| **DeepSeek V4 0731 Flash** | Q8_K_XL | The second opinion. Rotated in when I want a different brain on the same problem. |

The live server, verbatim from `ps`, because it's the most honest documentation in this repo:

```bash
llama-server -m .../Qwen3.8-Flash-Next-Q8_0-00001-of-00006.gguf \
  --port 35115 --parallel 4 --flash-attn on -c 180352 --kv-unified \
  --spec-default --jinja \
  --chat-template-kwargs '{"enable_thinking": true, "preserve_thinking": true}' \
  --mmproj .../mmproj-F16.gguf --fit on --fit-ctx 180352 --fit-target 512
```

~30 GB of weights, **180,352-token context**, four parallel slots, multimodal, and it
still leaves room to keep the desktop alive. Yes — this README was written by the box
itself, on the model in that command line. Meta enough for a home lab.

### NVFP4 Qwen3.8-27B on a single RTX 5090 — **~130 tok/s**

The party trick, and the reason this repo has a submodule instead of a shrug.

`vllm-sm120-nvfp4-mtp/` is a pinned community vLLM overlay (v0.27.1 + SM120 patch) that
gets ModelOpt **NVFP4** weights, **NVFP4 KV cache**, and **MTP-3 speculative decoding**
onto consumer Blackwell:

- **~130 tok/s decode** for the 27B NVFP4 checkpoint on one 5090.
- **373,797-token KV pool** with MTP-3 armed — 262K context is not a spec sheet number here, it's what actually fits.
- **704.4 aggregate tok/s at 8 concurrent streams**, every stream ≥ 92.8 tok/s.
- Byte-identical temperature-zero output, 9/9 needle-at-32K cold *and* prefix-cached, 8/8 tool calls plus 4/4 concurrent structured arguments, 21/21 vision lanes through a **CPU** vision tower so the vision encoder never eats VRAM.
- Every claim is checksummed and reproducible: immutable image digest, pinned model revision, `SHA256SUMS`, `EVIDENCE.md`.

Also hanging around in `models/qwen-3.8-27B-NVFP4/`: vLLM vs SGLang launch scripts,
DFlash/MTP variants, and the raw `guidellm` benchmark dumps that settled those arguments.

---

## 🎙️ The 5080's Job Description

### ASR stack — dictation that beats the cloud (`asr/`)

Replaced Whisprflow. A MacBook menu-bar app holds one hotkey; the rig does the rest.
One upload in, one cleaned transcript out. Non-streaming, on purpose.

```
MacBook ──POST /v1/transcribe──▶ asr-api :8090 (Go)
                                   ├─▶ Qwen3-ASR-1.7B          (qwen3-asr)
                                   └─▶ Qwen3-4B-Instruct-2507-FP8 (qwen3-cleanup)
```

| Piece | What it is | VRAM |
|---|---|---|
| `asr-model` | **Qwen3-ASR-1.7B** on vLLM, OpenAI `/v1/audio/transcriptions` | 6,094 MiB (util 0.42) · 12,800-tok KV |
| `cleanup-model` | **Qwen3-4B-Instruct-2507-FP8** — the *smaller language model*: punctuation, self-corrections, "um" removal, formatting at temp 0 | 6,698 MiB (util 0.45) · 13,104-tok KV |
| `asr-api` | Stdlib-only Go facade, non-root, bearer token optional, 25 MiB upload cap | — |

Measured warm: **11 s of audio → 343 ms** end to end (ASR 166 ms + cleanup 176 ms).
Cleanup dying never fails a request — you get `raw_text` back with a warning. Dictation
must survive the little model being down; only the ASR leg is load-bearing.

Two engines on one 16 GB card taught me things: they must come up **serially**
(`depends_on: service_healthy`, or vLLM's memory profiler throws
`AssertionError: Error in memory profiling`), and the stock vLLM image ships **without**
the `[audio]` extra, so every upload dies with a smug `400 Invalid or unsupported audio file`.
Hence `asr/model-image/Dockerfile`.

### Embeddings — `embeddings/`

`Qwen/Qwen3-Embedding-4B` (fp16) on Hugging Face **Text Embeddings Inference, CPU build**.
Deliberately off the GPUs: 24 Zen 4 cores are idle while the 5090 is busy thinking, and
embedding traffic should never compete with inference for Blackwell silicon.

---

## 🌍 How It Powers My Home World

- **Open WebUI** `:3000` → straight at Unsloth Studio on the host gateway. The kitchen-table face of the rig.
- **Unsloth Studio** `:8888` — model serving, slot save/restore, per-slot context, vision input.
- **LiteLLM** `:4000` (+ Postgres 16 for keys/spend/models) behind a profile: one `auto-local`
  endpoint with `least-busy` routing across vLLM and SGLang backends, health checks, and
  fallbacks. Kill a backend mid-sentence; the client never notices.
- **ASR** `:8090` — voice-to-text from the laptop, LAN-wide.
- **Embeddings** `:8080` — retrieval substrate for everything that needs memory.
- **Stealthy browser** — Camoufox (anti-detect Firefox) on a virtual display, CPU-only with Mesa
  software GL, JSON API + MCP server, noVNC for when I want to watch it work. Loopback-bound;
  agents reach it on the compose network. My agents get to browse without being fingerprinted,
  and I get to see exactly what they did. `docs/camoufox-plan.md` is the design doc.
- **Tailscale** — the rig is reachable from anywhere, and "anywhere" is still only me.

---

## 📦 Repo Map

```
ai-lab/
├── docker-compose.yml        # openwebui + profiles: vllm / litellm / agent-browser
├── Makefile                  # the only commands I remember
├── setup.sh                  # creates ai-lab-inference-net (do this first)
├── asr/                      # speech-to-text stack → RTX 5080 (own compose project, own net)
│   ├── api/                  # Go facade: /v1/transcribe, /healthz + 16 test funcs
│   └── model-image/          # vLLM overlay with the [audio] extra
├── embeddings/               # Qwen3-Embedding-4B on TEI (CPU)
├── litellm/litellm_config.yaml  # auto-local routing + fallbacks
├── models/qwen-3.8-27B-NVFP4/# vLLM/SGLang launch + benchmark scripts, results/
├── vllm-sm120-nvfp4-mtp/     # submodule: NVFP4-KV + MTP-3 build for the 5090
├── gpu-burn/                 # submodule: because new rigs must be burned in
├── benchmarking/             # guidellm env + sweep script
└── docs/camoufox-plan.md     # agent browsing design
```

## 🎛️ Commands

```bash
./setup.sh                    # shared docker network, once

make up                       # vLLM inference (profile: vllm)
make up-litellm               # + LiteLLM gateway & Postgres
make up-browser               # + Camoufox stealth browser
make asr-up                   # ASR stack → pinned to the 5080
make embeddings-up            # Qwen3-Embedding-4B (CPU)
make ps / logs / asr-logs     # staring at things

make asr-test                 # Go test suite, no GPU required

cd vllm-sm120-nvfp4-mtp && ./start.sh    # the 130 tok/s NVFP4 rig
./status.sh ; ./verify.sh --full         # gates: needle, determinism, vision, long-decode
```

### Ports

| Port | Service | Bound to |
|---|---|---|
| `8888` | Unsloth Studio (host-native) | `0.0.0.0` |
| `35115` | llama-server under Studio | loopback |
| `3000` | Open WebUI | LAN |
| `4000` | LiteLLM gateway | LAN (profile: litellm) |
| `8090` | ASR API — the only client-facing ASR surface | LAN |
| `8010 / 8011` | ASR + cleanup vLLM debug | loopback |
| `8080` | Qwen3-Embedding-4B (TEI) | LAN |
| `8900 / 5900` | Camoufox API / noVNC | loopback |

## 🔬 Proof, Not Vibes

Nothing here is "seems fast". `benchmarking/` runs `guidellm` concurrency sweeps against
both backends and dumps JSON/CSV into `models/qwen-3.8-27B-NVFP4/results/`. The NVFP4/MTP
release carries a checksummed evidence matrix (`EVIDENCE.md`) and CI integrity checks.
`gpu-burn` exists so that when something gets weird at 3 AM, I can prove it's silicon
before I blame the quantizer — which is usually the wrong answer, but occasionally isn't.

## ⚠️ Fine Print

This is a home lab on a desk I also sleep near. Endpoints are unauthenticated by default
and expect a trusted LAN: **do not** expose vLLM, LiteLLM, or `asr-api` to the public
internet without a token and a TLS-terminating proxy in front. Images are pinned where it
matters and floating where I'm lazy. Model weights are pulled from Hugging Face under their
own licenses and are never redistributed here. Submodules stay attributed upstream
(Apache-2.0 throughout).

If you wandered in looking for a deployable project: thanks, but the interesting part of
this repo is that it only ever had one user, one desk, and two GPUs that refuse to share.
