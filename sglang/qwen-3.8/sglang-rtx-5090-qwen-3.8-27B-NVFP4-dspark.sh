#!/bin/bash

docker run --gpus all \
  --shm-size 32g \
  -p 30000:30000 \
  -v ~/.cache/huggingface:/root/.cache/huggingface \
  --env "HF_TOKEN=${HF_TOKEN}" \
  --ipc=host \
  lmsysorg/sglang:qwen38-27b \
  sglang serve \
    --trust-remote-code \
    --model-path RadixArk/Qwen3.8-27B-NVFP4 \
    --mem-fraction-static 0.92 \
    --attention-backend flashinfer \
    --chunked-prefill-size 2048 \
    --reasoning-parser qwen3 \
    --tool-call-parser qwen3_coder \
    --speculative-algorithm DSPARK \
    --speculative-draft-model-path RadixArk/Qwen3.8-27B-DSpark \
    --mamba-full-memory-ratio 5 \
    --mamba-ssm-dtype bfloat16 \
    --max-running-requests 1 \
    --context-length 40000 \
    --host 0.0.0.0 \
    --port 30000
