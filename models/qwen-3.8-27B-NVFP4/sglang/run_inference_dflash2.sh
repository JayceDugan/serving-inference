#!/bin/bash

docker run --rm --gpus '"device=0"' \
  --shm-size 32g \
  --name sglang-inference \
  --network ai-lab-inference-net \
  -p 30000:30000 \
  -v ~/.cache/huggingface:/root/.cache/huggingface \
  --env "HF_TOKEN=${HF_TOKEN}" \
  --ipc=host \
  lmsysorg/sglang:dev-cu13 \
  sglang serve \
    --trust-remote-code \
    --model-path RadixArk/Qwen3.8-27B-NVFP4 \
    --kv-cache-dtype fp8_e4m3 \
    --mem-fraction-static 0.945 \
    --attention-backend flashinfer \
    --max-running-requests 1 \
    --cuda-graph-max-bs 1 \
    --reasoning-parser qwen3 \
    --tool-call-parser qwen3_coder \
    --mamba-full-memory-ratio 10 \
    --host 0.0.0.0 \
    --port 30000 \
    --speculative-algorithm DFLASH \
    --speculative-draft-model-path incoai/Qwen3.8-27B-DFlash2 \
    --speculative-num-draft-tokens 8 \
    --mamba-radix-cache-strategy extra_buffer_lazy \
    --mamba-ssm-dtype float32
