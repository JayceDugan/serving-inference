"""kokoro-model: Kokoro-82M text-to-speech behind an OpenAI-style endpoint.

    POST /v1/audio/speech   {"input": "text", "voice": "af_heart", "speed": 1}
                            -> 200 audio/wav (PCM16, 24 kHz mono)
    GET  /health            -> 200 once the pipeline is loaded

One KPipeline per process (one language; KOKORO_LANG_CODE, default 'a' =
American English). Generation runs under a lock: KModel is not thread-safe
and this is a solo-user service — serial requests are the expected load.
Voices are lazy-downloaded from hexgrad/Kokoro-82M on first use and cached;
the default voice is preloaded at startup so the first request is fast.
"""

import io
import os
import threading
import wave
from contextlib import asynccontextmanager

import numpy as np
import torch
import uvicorn
from fastapi import FastAPI, HTTPException
from fastapi.responses import Response
from huggingface_hub.errors import EntryNotFoundError, RepositoryNotFoundError
from pydantic import BaseModel, Field

SAMPLE_RATE = 24000
DEFAULT_VOICE = "af_heart"
REPO_ID = "hexgrad/Kokoro-82M"

_pipeline = None
_gen_lock = threading.Lock()


class SpeechRequest(BaseModel):
    # OpenAI-compatible request shape; `model` is accepted and ignored.
    model: str | None = None
    input: str
    voice: str = DEFAULT_VOICE
    speed: float = Field(default=1.0, ge=0.25, le=4.0)


def _load_pipeline():
    from kokoro import KPipeline

    lang_code = os.environ.get("KOKORO_LANG_CODE", "a")
    pipeline = KPipeline(lang_code=lang_code, repo_id=REPO_ID)  # auto cuda
    pipeline.load_voice(DEFAULT_VOICE)  # warm the default voice download
    return pipeline


@asynccontextmanager
async def lifespan(_: FastAPI):
    global _pipeline
    _pipeline = _load_pipeline()
    yield


app = FastAPI(title="kokoro-model", lifespan=lifespan)


@app.get("/health")
def health() -> dict:
    if _pipeline is None:
        raise HTTPException(status_code=503, detail="pipeline loading")
    return {"status": "ok"}


@app.post("/v1/audio/speech")
def speech(req: SpeechRequest) -> Response:
    text = req.input.strip()
    if not text:
        raise HTTPException(status_code=400, detail="input must be non-empty text")

    with _gen_lock:
        try:
            results = list(_pipeline(text, voice=req.voice, speed=req.speed))
        except (EntryNotFoundError, RepositoryNotFoundError) as e:
            raise HTTPException(
                status_code=400, detail=f"unknown voice {req.voice!r}: {e}"
            ) from e
        except Exception as e:  # G2P failure, CUDA error, ...
            raise HTTPException(
                status_code=500, detail=f"kokoro generation failed: {e}"
            ) from e

    chunks = [r.audio for r in results if r.audio is not None]
    if not chunks:
        raise HTTPException(status_code=422, detail="no audio generated")

    # KModel emits float32 mono at 24 kHz, roughly in [-1, 1].
    pcm16 = (torch.cat(chunks).clamp(-1.0, 1.0).numpy() * 32767.0).astype(np.int16)

    buf = io.BytesIO()
    with wave.open(buf, "wb") as wav:
        wav.setnchannels(1)
        wav.setsampwidth(2)  # 16-bit PCM
        wav.setframerate(SAMPLE_RATE)
        wav.writeframes(pcm16.tobytes())

    return Response(content=buf.getvalue(), media_type="audio/wav")


if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=8000, log_level="info")
