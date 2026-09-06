FROM python:3.11-slim

WORKDIR /app

# Install system dependencies needed for mathematical libraries
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Install lightweight dependencies for execution
RUN pip install --no-cache-dir \
    fastapi \
    uvicorn \
    pydantic \
    transformers \
    onnxruntime \
    huggingface_hub

# CPU-only torch (used solely for mean pooling in app.py). pytorch.org is not a
# pip index, so this needs the wheel index; --index-url must be its own install
# or it would replace PyPI for every package above.
RUN pip install --no-cache-dir torch --index-url https://download.pytorch.org/whl/cpu

COPY app.py .

EXPOSE 8080

CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8080"]

