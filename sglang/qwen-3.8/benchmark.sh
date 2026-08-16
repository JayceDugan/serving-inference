#!/bin/bash

docker exec -it sglang_inference python3 -m sglang.bench_serving \
  --backend sglang --port 30000 \
  --dataset-name random \
  --random-input-len 8192 --random-output-len 512 \
  --random-range-ratio 0.5 \
  --num-prompts 30 --max-concurrency 3
