# Thin overlay on the official vLLM image: the base ships without the
# [audio] extra, so /v1/audio/transcriptions 400s on every upload
# ("Please install vllm[audio]"). Extras are pinned to whatever vLLM
# version the base image carries — no resolver drift against torch.
ARG VLLM_BASE=vllm/vllm-openai:latest
FROM ${VLLM_BASE}

RUN V=$(python3 -c "from importlib.metadata import version; print(version('vllm'))") \
    && pip install --no-cache-dir "vllm[audio]==${V}"
