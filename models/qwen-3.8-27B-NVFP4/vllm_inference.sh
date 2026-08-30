#!/bin/bash

docker run --rm --runtime nvidia --gpus all \
    -v ~/.cache/huggingface:/root/.cache/huggingface \
    --env "HF_TOKEN=${HF_TOKEN}" \
    -p 8000:8000 \
    --ipc=host \
    --name vllm-inference \
    --network ai-lab-inference-net \
    vllm/vllm-openai:latest \
	--model unsloth/Qwen3.8-27B-NVFP4 \
	--quantization compressed-tensors \
	--kv-cache-dtype fp8 \
	--max-model-len 131072 \
	--gpu-memory-utilization 0.97 \
	--max-num-seqs 4 \
	--speculative-config '{"method":"mtp","num_speculative_tokens":3}' \
	--enable-prefix-caching \
	--enable-auto-tool-choice \
	--tool-call-parser qwen3_coder \
	--reasoning-parser qwen3
