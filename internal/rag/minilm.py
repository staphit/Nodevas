"""Private stdin/stdout helper, embedded in the Nodevas binary."""
import contextlib
import json
import sys
from pathlib import Path

_model = None

def embed(texts):
    import numpy as np
    from chromadb.utils.embedding_functions import ONNXMiniLM_L6_V2

    # Keep the tokenizer and ONNX session warm across bounded requests.
    global _model
    if _model is None:
        _model = ONNXMiniLM_L6_V2(preferred_providers=["CPUExecutionProvider"])
        _model._download_model_if_not_exists()
        # Bound CPU use as well as tensor memory. The pinned Chroma helper's
        # default ONNX session otherwise creates a pool sized to the host.
        options = _model.ort.SessionOptions()
        options.intra_op_num_threads = 2
        options.inter_op_num_threads = 1
        options.log_severity_level = 3
        options.graph_optimization_level = _model.ort.GraphOptimizationLevel.ORT_ENABLE_ALL
        _model.model = _model.ort.InferenceSession(
            str(Path(_model.DOWNLOAD_PATH) / _model.EXTRACTED_FOLDER_NAME / "model.onnx"),
            sess_options=options, providers=["CPUExecutionProvider"])
    model = _model
    tokenizer = model.tokenizer
    tokenizer.no_truncation()
    tokenizer.no_padding()
    windows = []
    spans = []
    for text in texts:
        tokens = tokenizer.encode(text, add_special_tokens=False).ids
        # MiniLM accepts 256 tokens including CLS/SEP. Encode every window
        # rather than silently discarding the end of a long document chunk.
        start = len(windows)
        windows.extend([tokenizer.decode(tokens[i:i + 240]) for i in range(0, len(tokens), 240)] or [""])
        spans.append((start, len(windows)))
    tokenizer.enable_truncation(max_length=256)
    tokenizer.enable_padding(pad_id=0, pad_token="[PAD]", length=256)
    # Keep ONNX's activation arena small; its default 32-window inference batch
    # dominates peak memory even when transport batches are already bounded.
    vectors = np.concatenate([np.asarray(model(windows[i:i + 8]))
                              for i in range(0, len(windows), 8)], axis=0)
    result = []
    for start, end in spans:
        vector = np.mean(vectors[start:end], axis=0)
        norm = np.linalg.norm(vector)
        if not np.isfinite(norm) or norm == 0:
            raise ValueError("MiniLM returned an invalid vector")
        result.append((vector / norm).tolist())
    return result


if __name__ == "__main__":
    for line in sys.stdin:
        texts = json.loads(line)
        if not isinstance(texts, list) or len(texts) > 32 or not all(isinstance(text, str) for text in texts):
            raise ValueError("expected a batch of at most 32 strings")
        # Model/library diagnostics must never corrupt the JSON response.
        with contextlib.redirect_stdout(sys.stderr):
            vectors = embed(texts)
        sys.stdout.write(json.dumps(vectors, allow_nan=False, separators=(",", ":")) + "\n")
        sys.stdout.flush()
