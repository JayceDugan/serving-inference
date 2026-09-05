# Promptfoo prompt evaluation

Evals for the cleanup model (`asr-cleanup-model`, vLLM on loopback port 8011),
graded by the judge model (Unsloth Studio on `localhost:8888`).

## Prompt under test

The canonical system prompt is [`../prompts/cleanup-system.txt`](../prompts/cleanup-system.txt),
loaded by `asr-api` at startup (`CLEANUP_PROMPT_FILE`).

promptfoo v0.122 only accepts chat-message prompts as a `file://` JSON file, so
the same text is also embedded in [`prompts/cleanup.json`](prompts/cleanup.json)
(messages array: system + `{{transcript}}`). **When you change the prompt, edit
both files** — keep them byte-identical (modulo whitespace). Then:

- service: `docker compose --profile asr restart asr-api`
- evals: just re-run — no rebuild needed

## Prerequisites

1. The ASR stack is up (cleanup model healthy):

```bash
docker compose --profile asr ps
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

Smoke tests that prove the wiring (cleanup model hit, judge grading):

| case | input shape | assertions |
|---|---|---|
| 1 | fillers (`um`, `uh`) | content preserved; fillers removed |
| 2 | repeated phrase | content preserved; output not bloated |
| 3 | clean sentence | `llm-rubric`: cleaned transcript, no preamble/quotes (exercises the judge) |

Prompt-quality regressions (e.g. the model answering questions contained in the
transcript) get added here as the system prompt is iterated on.

## Learn more

- Configuration guide: https://promptfoo.dev/docs/configuration/guide
- All providers: https://promptfoo.dev/docs/providers
- Assertions & metrics: https://promptfoo.dev/docs/configuration/expected-outputs
