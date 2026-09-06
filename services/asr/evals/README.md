# Promptfoo prompt evaluation

Evals for the cleanup model (`asr-cleanup-model`, vLLM on loopback port 8011),
graded by the judge model (Unsloth Studio on `localhost:8888`).

## Prompt under test

The canonical system prompt is [`../prompts/cleanup-system.txt`](../prompts/cleanup-system.txt),
loaded by `asr-api` at startup (`CLEANUP_PROMPT_FILE`).

s1-mini only follows its trained input shape: that exact system prompt, then a
control line + newline + the raw transcript as the user message. asr-api builds
the control line from `CLEANUP_STYLING` (default `semi-formal`) plus fixed
`Structure: prose` / `Context: general`; this suite hard-codes the same
semi-formal control line in [`prompts/cleanup.json`](prompts/cleanup.json).
**When you change the prompt or the control line, edit both files** — keep them
in sync (modulo whitespace). Then:

- service: `docker compose restart asr-api`
- evals: just re-run — no rebuild needed

Thinking must stay off for s1-mini (`enable_thinking: false`). The vLLM server
sets it by default (`--default-chat-template-kwargs` in docker-compose.yml) and
asr-api also sends it per request; without it the model emits an empty think
block and stops.

## Prerequisites

1. The ASR stack is up (cleanup model healthy):

```bash
docker compose ps asr-model cleanup-model asr-api
```

2. Export the judge's auth key:

```bash
export UNSLOTH_API_KEY=...   # required by Unsloth Studio on :8888
```

## Run

```bash
npm run eval          # = promptfoo eval --config evals/promptfooconfig.yaml
```

View results in the browser:

```bash
promptfoo view
```

## Current suite

Semi-formal register: full capitalization + punctuation, contractions kept,
colloquialisms smoothed. Expected substrings are the model card's measured
outputs under this exact control line (greedy decoding).

| case | input shape | assertions |
|---|---|---|
| 1 | fillers (`um`) | content preserved; fillers removed |
| 2 | self-correction (`forty two no sorry forty three`) | resolves to `43`; no spelled-out numbers |
| 3 | spoken time | ITN → `3:15pm` |
| 4 | spoken currency + date | ITN → `$23,450`, `March 3, 2026` |
| 5 | spoken email address | ITN → `support@superwhisper.com` |
| 6 | colloquialisms (`gonna`) | smoothed to `going to`; contractions kept; sentence case |
| 7 | repeated phrase | collapsed; output not bloated |
| 8 | clean lowercase sentence | sentence case + final period |
| 9 | filler-only input | empty output (s1-mini's correct result for noise) |
| 10 | question in transcript | normalized, not answered |
| 11 | clean sentence | `llm-rubric`: semi-formal cleaned transcript, no preamble/quotes (exercises the judge) |

Prompt-quality regressions get added here as the system prompt is iterated on.

## Learn more

- Configuration guide: https://promptfoo.dev/docs/configuration/guide
- All providers: https://promptfoo.dev/docs/providers
- Assertions & metrics: https://promptfoo.dev/docs/configuration/expected-outputs
