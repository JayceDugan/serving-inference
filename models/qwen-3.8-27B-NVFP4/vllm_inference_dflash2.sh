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
	--kv-cache-dtype nvfp4 \
	--max-model-len 131072 \
	--gpu-memory-utilization 0.97 \
	--max-num-seqs 8 \
	--speculative-config '{"method":"dflash","model":"YourHighnessLA/Qwen3.8-27B-DFlash2-NVFP4", "num_speculative_tokens":3, "kv_cache_dtype":"nvfp4"}' \
	--enable-prefix-caching \
	--enable-auto-tool-choice \
	--tool-call-parser qwen3_coder \
	--reasoning-parser qwen3 \
	--compilation-config '{"cudagraph_mode":"FULL_AND_PIECEWISE", "cudagraph_capture_sizes":[4,8,12,16,20,24,28,32]}'

