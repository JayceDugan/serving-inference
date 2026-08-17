#!/bin/bash

docker run --runtime nvidia --gpus all \
    -v ~/.cache/huggingface:/root/.cache/huggingface \
    --env "HF_TOKEN=${HF_TOKEN}" \
    -p 8000:8000 \
    --ipc=host \
    vllm/vllm-openai:latest \
    --model unsloth/Qwen3.8-27B-NVFP4 \
    --kv-cache-dtype fp8 \
    --trust-remote-code \
    --max-model-len 132067 \
    --max-num-seqs 16 \
    --gpu-memory-utilization 0.97 \
    --reasoning-parser qwen3 \
    --enable-auto-tool-choice \
    --tool-call-parser qwen3_xml

