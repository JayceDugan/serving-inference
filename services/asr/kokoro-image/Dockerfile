# kokoro-model: Kokoro-82M TTS behind an OpenAI-style /v1/audio/speech.
#
# Plain Python + pip torch: the wheels bundle their own CUDA runtime (cu13 as
# of 2.14), so no CUDA toolkit is needed in the image — same trick as the vLLM
# base image. espeak-ng is required for misaki's English OOD-word fallback;
# libsndfile backs soundfile (voice .pt files are plain torch tensors, but the
# dependency comes with kokoro's tooling).
FROM python:3.12-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends curl espeak-ng libsndfile1 \
    && rm -rf /var/lib/apt/lists/*

# >=2.9 guarantees a Blackwell-capable (sm_120) CUDA wheel; <3 keeps rebuilds
# on the 2.x line kokoro is built against.
RUN pip install --no-cache-dir "torch>=2.9,<3" \
    && pip install --no-cache-dir "kokoro==0.9.4" soundfile fastapi uvicorn

COPY server.py /app/server.py
WORKDIR /app
EXPOSE 8000
ENTRYPOINT ["python", "-u", "/app/server.py"]
