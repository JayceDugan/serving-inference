# 🖥️ The War Rig — Jayce's Home AI Lab

> **Not a product. Not a framework. Nothing here is packaged for you to deploy.**
> This is my house, my desk, my power bill, and the machine that thinks in it.
> It exists so I can point at something on my wall and say *that box runs my world*.

<p align="center">
  <img src="docs/assets/rig.jpg" alt="The devbox — Threadripper PRO workstation with an RTX 5090 and an RTX 5080" width="720">
</p>

<p align="center"><em>The rig. Two Blackwell cards, one desk, no cloud in sight.</em></p>

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
│                                                  │   │  Never touches the 5090. Ever.    │
└──────────────────────────────────────────────────┘   └───────────────────────────────────┘
```

The rule: **nothing in the ASR path may reserve a byte of the 5090.**
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

---

## 🎙️ The 5080's Job Description

### ASR stack — dictation that beats the cloud (`services/asr/`)

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
Hence `services/asr/model-image/Dockerfile`.

---

## 🌍 How It Powers My Home World

- **Open WebUI** `:3000` → straight at Unsloth Studio on the host gateway. The kitchen-table face of the rig.
- **Unsloth Studio** `:8888` — model serving, slot save/restore, per-slot context, vision input.
- **ASR** `:8090` — voice-to-text from the laptop, LAN-wide.
- **Stealthy browser** — Camoufox (anti-detect Firefox) on a virtual display, CPU-only with Mesa
  software GL, JSON API + MCP server, noVNC for when I want to watch it work. Loopback-bound;
  agents reach it on the compose network. My agents get to browse without being fingerprinted,
  and I get to see exactly what they did. `docs/camoufox-plan.md` is the design doc.
- **Tailscale** — the rig is reachable from anywhere, and "anywhere" is still only me.

---

## 📦 Repo Map

```
ai-lab/
├── docker-compose.yml        # single stack, all 7 services (profiles: unsloth, embedding, agent-browser, asr)
├── Makefile                  # the only commands I remember
├── .env / .env.example       # root + ASR keys; docker compose reads .env automatically
├── workspaces/
│   └── unsloth/              # mounted into the unsloth container at /workspace/host
├── services/
│   ├── asr/                  # speech-to-text stack → RTX 5080 (profile: asr, own net)
│   │   ├── api/              # Go facade: /v1/transcribe, /healthz + 16 test funcs
│   │   └── model-image/      # vLLM overlay with the [audio] extra
│   └── embeddings/           # ONNX embedding service (profile: embedding)
├── tools/
│   └── gpu-burn/             # submodule: because new rigs must be burned in
└── docs/                     # camoufox-plan.md (agent browsing design), assets/rig.jpg
```

## 🎛️ Commands

```bash
make up                       # Open WebUI
make up-browser               # + Camoufox stealth browser
make asr-up                   # ASR stack → pinned to the 5080
make ps / logs / asr-logs     # staring at things

make asr-test                 # Go test suite, no GPU required
```

### Ports

| Port | Service | Bound to |
|---|---|---|
| `8888` | Unsloth Studio (host-native) | `0.0.0.0` |
| `35115` | llama-server under Studio | loopback |
| `3000` | Open WebUI | LAN |
| `8090` | ASR API — the only client-facing ASR surface | LAN |
| `8010 / 8011` | ASR + cleanup vLLM debug | loopback |
| `8900 / 5900` | Camoufox API / noVNC | loopback |

## 🔬 Proof, Not Vibes

`gpu-burn` exists so that when something gets weird at 3 AM, I can prove it's silicon
before I blame the quantizer — which is usually the wrong answer, but occasionally isn't.

## ⚠️ Fine Print

This is a home lab on a desk I also sleep near. Endpoints are unauthenticated by default
and expect a trusted LAN: **do not** expose Open WebUI or `asr-api` to the public
internet without a token and a TLS-terminating proxy in front. Images are pinned where it
matters and floating where I'm lazy. Model weights are pulled from Hugging Face under their
own licenses and are never redistributed here. Submodules stay attributed upstream
(Apache-2.0 throughout).

If you wandered in looking for a deployable project: thanks, but the interesting part of
this repo is that it only ever had one user, one desk, and two GPUs that refuse to share.
