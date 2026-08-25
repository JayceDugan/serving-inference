#!/bin/bash

docker run --rm --gpus '"device=0"' \
  --shm-size 32g \
  --name sglang-inference \
  --network ai-lab-inference-net \
  -p 30000:30000 \
  -v ~/.cache/huggingface:/root/.cache/huggingface \
  --env "HF_TOKEN=${HF_TOKEN}" \
  --ipc=host \
  lmsysorg/sglang:qwen38-27b \
  sglang serve \
    --trust-remote-code \
    --model-path RadixArk/Qwen3.8-27B-NVFP4 \
    --mem-fraction-static 0.95 \
    --attention-backend flashinfer \
    --chunked-prefill-size 2048 \
    --reasoning-parser qwen3 \
    --tool-call-parser qwen3_coder \
    --host 0.0.0.0 \
    --port 30000
