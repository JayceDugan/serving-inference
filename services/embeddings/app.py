import os
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from transformers import AutoTokenizer
import onnxruntime as ort
import torch

app = FastAPI(title="Qwen3 ONNX Embedding Server")

# Get configuration from environment variables (with Threadripper defaults)
MODEL_ID = os.getenv("MODEL_ID", "onnx-community/Qwen3-Embedding-4B-ONNX")
NUM_THREADS = int(os.getenv("OMP_NUM_THREADS", "8")) 

# Configure ONNX Runtime session options for Threadripper NUMA architecture
sess_options = ort.SessionOptions()
sess_options.intra_op_num_threads = NUM_THREADS
sess_options.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL

print(f"Loading ONNX model '{MODEL_ID}' with {NUM_THREADS} threads...")

# Initialize Tokenizer and ONNX Session
# Pointing local paths ensures it respects the mounted cache volume
tokenizer = AutoTokenizer.from_pretrained(MODEL_ID, local_files_only=True)

# Find the model.onnx file inside the Hugging Face hub structure
# (Handled dynamically via huggingface_hub cache structure inside the container)
from huggingface_hub import hf_hub_download
model_path = hf_hub_download(repo_id=MODEL_ID, filename="model.onnx", local_files_only=True)
session = ort.InferenceSession(model_path, sess_options, providers=["CPUExecutionProvider"])

class EmbedRequest(BaseModel):
    inputs: str

@app.post("/embed")
async def embed(request: EmbedRequest):
    try:
        # Tokenize text input
        encoded_input = tokenizer(
            [request.inputs], 
            padding=True, 
            truncation=True, 
            max_length=512, 
            return_tensors="np"
        )
        
        # Prepare inputs for ONNX runtime
        onnx_inputs = {
            "input_ids": encoded_input["input_ids"].astype("int64"),
            "attention_mask": encoded_input["attention_mask"].astype("int64")
        }
        
        # Run inference
        outputs = session.run(None, onnx_inputs)
        
        # Apply Mean Pooling to generate the final embedding vector
        token_embeddings = torch.from_numpy(outputs[0])
        attention_mask = torch.from_numpy(onnx_inputs["attention_mask"])
        
        input_mask_expanded = attention_mask.unsqueeze(-1).expand(token_embeddings.size()).float()
        sum_embeddings = torch.sum(token_embeddings * input_mask_expanded, 1)
        sum_mask = torch.clamp(input_mask_expanded.sum(1), min=1e-9)
        
        embedding = (sum_embeddings / sum_mask).squeeze(0).tolist()
        
        return {"embedding": embedding}
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@app.get("/health")
async def health():
    return {"status": "healthy"}

