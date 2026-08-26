# Camoufox self-hosting plan

Goal: add private, anti-detect browsing to the ai-lab stack so agents
(vLLM/LiteLLM `auto-local`, Open WebUI, or any agent harness) can browse the
web without fingerprinting/bot detection, fully self-hosted on
`ai-lab-inference-net`.

## What Camoufox is

- Open-source Firefox fork ([daijro/camoufox](https://github.com/daijro/camoufox),
  MIT) that spoofs fingerprint signals at the **C++ implementation level**
  (`navigator.*`, WebGL renderer, AudioContext, screen geometry, WebRTC) —
  no JS shims. Every session gets a fresh identity drawn from real-world
  device distributions.
- Ships as prebuilt binaries on GitHub Releases (~300 MB, `lin.x86_64` /
  `lin.arm64`). PyPI package `camoufox` (latest 0.5.5) wraps it with a
  Playwright API and a `python -m camoufox server` WebSocket mode (experimental).
- The `Dockerfile` in the camoufox repo is for **building Firefox from source**
  — not useful for running it.

### Docker-relevant facts (researched)

| Fact | Implication |
|---|---|
| True `headless=True` mode "may still be detectable in the future" | Run under **Xvfb virtual display** (`headless="virtual"`), not pure headless |
| WebGL context creation fails without Mesa/llvmpipe in containers — "a major bot detection signal" | Image must include `libegl1 libgl1-mesa-dri libgbm1` |
| Firefox needs >64 MB `/dev/shm` | `--shm-size=2g` / `shm_size: 2g` in compose |
| Official `launch_server` WebSocket mode is one-browser-per-server | Fingerprints don't rotate between clients; each server process = one identity |
| Browser needs no GPU | CPU-only; Mesa software rendering. Budget ~1–5 GB RAM per instance |
| uBlock Origin + privacy addons ship with it (uBlock Origin, LocalCDN, ClearURLs, Consent-O-Matic in the stealthy-auto-browse image) | Privacy baked in |

## Options

### A. `psyb0t/stealthy-auto-browse` (public Docker Hub image) — **recommended**

[github.com/psyb0t/docker-stealthy-auto-browse](https://github.com/psyb0t/docker-stealthy-auto-browse)
(WTFPL, 28k+ pulls, updated 2026-08-23)

- Runs Camoufox **on Xvfb** (virtual display, not headless) — better stealth.
- **Zero CDP exposure** (Firefox build without CDP; detectors have no CDP signals to see).
- Real **OS-level mouse/keyboard via PyAutoGUI** (browser sees genuine input).
- JSON **HTTP API on :8080** + **MCP server at `/mcp`** (Streamable HTTP) — any
  MCP-capable agent can drive it directly.
- **noVNC on :5900** to watch/debug the browser visually.
- Bearer-token auth via `AUTH_TOKEN`; cluster mode (HAProxy + Redis) later if
  concurrency is needed.
- Preinstalled addons: uBlock Origin, LocalCDN, ClearURLs, Consent-O-Matic.
- Extra: screen recording (ffmpeg), virtual camera/mic, YAML script mode.
- Reported to pass CreepJS, BrowserScan, Pixelscan, Cloudflare, SannySoft,
  Rebrowser, Incolumitas.
- Single instance serializes requests (fine for a personal lab).

Trade-offs: less "LLM-token-optimized" than option B (you get page text /
selectors / screenshots rather than compact accessibility snapshots with
element refs); one active task at a time on a single instance.

### B. `jo-inc/camofox-browser` (build from source) — best agent ergonomics

[github.com/jo-inc/camofox-browser](https://github.com/jo-inc/camofox-browser)
(MIT). Node REST API (port 9377) purpose-built for LLM agents:

- **Accessibility snapshots ~90% smaller than HTML** with stable element refs
  (`e1`, `e2`) — cheapest possible loop for our 27B local model.
- Per-user sessions, cookie import, persisted storage state, session tracing
  (Playwright trace zips), VNC plugin, proxy + GeoIP, search macros
  (`@google_search`, `@reddit_search`, …), YouTube transcripts (yt-dlp).
- MCP tools + OpenClaw plugin (`@askjo/camofox-browser`).
- Lazy browser launch, ~40 MB idle.
- **No public Docker image** (Docker Hub: 0 results) — build from the repo's
  Dockerfile (bakes Camoufox 135.0.1-beta.24; `make up` handles arch + fetch).
  Fits our `.gitmodules` convention: add as a git submodule.
- ⚠️ Sends **crash telemetry to the vendor's Cloudflare worker by default** —
  set `CAMOFOX_CRASH_REPORT_ENABLED=false` for a private stack.

### C. DIY: official `python -m camoufox server` (fallback)

Ubuntu + `pip install camoufox` + `camoufox fetch` + Xvfb entrypoint →
Playwright-protocol WebSocket server; clients connect with
`playwright.firefox.connect('ws://host:port/path')`. Most control, but the
server mode is explicitly *experimental* (undocumented Playwright internals),
one identity per server process, and it's the most glue code. The
[Web Scraping Club article](https://substack.thewebscraping.club/p/how-to-create-camoufox-docker-image)
has a working recipe (Xvfb :99, `launch_server(headless=True, geoip=True)`).

## Recommended integration (option A)

### Ports

| Port | Service |
|---|---|
| 8000 | vLLM (existing) |
| 4000 | LiteLLM (existing) |
| 3000 | Open WebUI (existing) |
| **8900** | stealthy-browser HTTP API + MCP |
| **5900** | noVNC viewer (optional, loopback-only) |

### `docker-compose.yml` additions

> **Status: implemented** (image pinned to `v2.6.7`, the 2026-08-23 release;
> verified end-to-end: health, HTTP API smoke test on bot.sannysoft.com —
> WebDriver missing/passed, Firefox UA — and MCP `tools/list` returning 18
> tools).

```yaml
  stealthy-browser:
    image: psyb0t/stealthy-auto-browse:v2.6.7   # pinned
    container_name: stealthy-browser
    # Browser is a 5GB-class process (Xvfb + Camoufox + Mesa software GL)
    shm_size: "2g"
    ports:
      # loopback-only: agents on the docker network reach it by service name;
      # remove the host ports entirely if no host-side access is wanted
      - "127.0.0.1:8900:8080"
      - "127.0.0.1:5900:5900"
    environment:
      AUTH_TOKEN: ${BROWSER_AUTH_TOKEN:-}
      # PROXY_HOST / PROXY_PORT / PROXY_USERNAME / PROXY_PASSWORD  # optional later
    healthcheck:
      test: ["CMD-SHELL", "python3 -c 'import urllib.request,sys; sys.exit(0 if urllib.request.urlopen(\"http://127.0.0.1:8080/health\", timeout=5).status==200 else 1)'"]
      interval: 30s
      timeout: 10s
      start_period: 90s
      retries: 10
    networks:
      - ai-lab-inference-net
```

Host-port and healthcheck choices verified: 8900/5900 were free on the host
at setup time; `/health` is the auth-exempt readiness endpoint per the v2.6.7
`docs/api.md`.

### `.env.example` additions

```env
# stealthy-auto-browse (private browsing for agents)
BROWSER_AUTH_TOKEN=
```

### `Makefile` additions (added: `up-browser`, `down-browser`, `logs-browser`)

```make
up-browser: ## Start the stack plus the stealth browser
	docker compose up -d stealthy-browser

logs-browser: ## Follow stealth browser logs
	docker compose logs -f stealthy-browser
```

### Agent wiring

1. **Any MCP-capable agent** (Claude Code, Codex, OpenClaw, custom harness):
   point it at `http://127.0.0.1:8900/mcp` (host) or
   `http://stealthy-browser:8080/mcp` (from a container) with
   `Authorization: Bearer $BROWSER_AUTH_TOKEN`.
2. **Open WebUI / litellm `auto-local`**: the LLM has no native browser tool;
   the practical routes are
   - give the agent a bash tool and document the HTTP API
     (`POST http://stealthy-browser:8080` with `{"action": ...}`), or
   - run the agent harness on the host where the MCP endpoint is reachable.
3. **Session semantics ("private browsing")**: each browser session carries a
   fresh Camoufox identity. For per-task privacy use a fresh session; for
   durable logins use its persistent-profile option (check
   `docs/configuration.md` of the pinned version). Cookies + uBlock Origin
   etc. are isolated from the host entirely.

## Alternative integration (option B, if token efficiency wins)

```yaml
  camofox:
    image: camofox-browser:135.0.1-x86_64   # built locally via the repo Makefile
    # build:
    #   context: ./camofox-browser            # git submodule (see .gitmodules)
    #   args: { CAMOUFOX_VERSION: "135.0.1", CAMOUFOX_RELEASE: "beta.24", ARCH: "x86_64" }
    container_name: camofox-browser
    shm_size: "2g"
    ports:
      - "127.0.0.1:9377:9377"
    environment:
      CAMOFOX_ACCESS_KEY: ${BROWSER_AUTH_TOKEN:-}   # bearer auth on all routes
      CAMOFOX_API_KEY: ${CAMOFOX_API_KEY:-}          # enables cookie import
      CAMOFOX_CRASH_REPORT_ENABLED: "false"          # no vendor telemetry
    volumes:
      - camofox_data:/home/node/.camofox             # cookies/profiles/traces
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:9377/health"]
      interval: 30s
      timeout: 10s
      retries: 5
    networks:
      - ai-lab-inference-net
```

Setup: `git submodule add https://github.com/jo-inc/camofox-browser camofox-browser`,
then `make -C camofox-browser build` (or `make up` once) before compose start.

## Hardening checklist (both options)

- [ ] Pin image to a release tag (no `:latest`)
- [ ] Set `BROWSER_AUTH_TOKEN` (long random: `openssl rand -base64 32`)
- [ ] Bind host ports to `127.0.0.1` (or drop them; compose-network access
      needs no host port)
- [ ] `CAMOFOX_CRASH_REPORT_ENABLED=false` (option B only)
- [ ] No GPU passthrough needed; CPU-only — verify host has ~5 GB free RAM
      before first real session
- [ ] Optional later: residential proxy + GeoIP (`PROXY_*`) for geo-matched
      locale/timezone/coordinates

## Risks / open questions

- **Single-instance concurrency**: one active browsing task at a time on
  stealthy-auto-browse (requests serialize). Fine for a single agent; for
  parallel agents use its cluster compose (HAProxy + Redis) or N replicas.
- **Image freshness**: both Camoufox (beta releases) and these wrappers move
  fast — pin versions and re-verify detection results on upgrade (their
  READMEs list the test suites; quick re-check on
  pixelscan.net / bot.sannysoft.com).
- **RAM**: vLLM + Open WebUI + a 5 GB-class browser on one host — check
  `free -g` before enabling; browser is the only CPU-side consumer.
- **Legal/ToS**: browsing on behalf of agents on real sites can violate site
  ToS; keep usage to research/automation that's acceptable.

## Verification plan

```bash
# 1. Stack up
docker compose up -d stealthy-browser

# 2. Health
TOKEN=$(grep BROWSER_AUTH_TOKEN .env | cut -d= -f2)
curl -fs http://127.0.0.1:8900/health

# 3. Smoke test via API
curl -fs -X POST http://127.0.0.1:8900 \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"action":"goto","url":"https://bot.sannysoft.com"}'
curl -fs -X POST http://127.0.0.1:8900 \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"action":"get_text"}' | head

# 4. Visual: open http://127.0.0.1:5900 (noVNC)
# 5. Fingerprint check: https://pixelscan.net  (expect: undetected, Firefox)
# 6. Agent check: connect an MCP client to http://127.0.0.1:8900/mcp and
#    drive a tab end-to-end
```

## Sources

- https://github.com/daijro/camoufox (+ camoufox.com docs: /python/usage, /python/remote-server, /python/virtual-display)
- https://github.com/psyb0t/docker-stealthy-auto-browse (Docker Hub: psyb0t/stealthy-auto-browse)
- https://github.com/jo-inc/camofox-browser
- https://substack.thewebscraping.club/p/how-to-create-camoufox-docker-image
