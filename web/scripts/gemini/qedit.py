"""Local character edits with Qwen-Image-Edit-2511 (Lightning 4 steps) through ComfyUI's API.

    python qedit.py jobs.json [--server http://127.0.0.1:8188]

jobs.json: [{"name": "deepseek_desk-type", "image": "keys/deepseek_base_green.jpg",
             "image2": optional second reference, "prompt": "...", "out": "dir", "seed": 1}]
Writes <out>/<name>.png. Mirrors the user's ComfyUI workflow 「AI短劇-角色狀態編輯」
(GGUF Q4_K_S unet + Lightning LoRA, AuraFlow shift 3.1, CFGNorm, cfg 1, euler/simple).
"""
import json
import os
import sys
import time
import urllib.parse
import urllib.request
import uuid

SERVER = "http://127.0.0.1:8188"
NEG = "blurry, lowres, deformed, extra fingers, extra limbs, different person, different face, different outfit, text, watermark, realistic, 3d"


def upload(path):
    boundary = uuid.uuid4().hex
    name = f"{uuid.uuid4().hex[:8]}_{os.path.basename(path)}"
    data = open(path, "rb").read()
    body = (f"--{boundary}\r\nContent-Disposition: form-data; name=\"image\"; filename=\"{name}\"\r\n"
            f"Content-Type: application/octet-stream\r\n\r\n").encode() + data + \
        f"\r\n--{boundary}\r\nContent-Disposition: form-data; name=\"overwrite\"\r\n\r\ntrue\r\n--{boundary}--\r\n".encode()
    req = urllib.request.Request(f"{SERVER}/upload/image", data=body,
                                 headers={"Content-Type": f"multipart/form-data; boundary={boundary}"})
    return json.loads(urllib.request.urlopen(req).read())["name"]


def graph(img1, prompt, seed, img2=None, megapixels=1.0):
    g = {
        "1": {"class_type": "UnetLoaderGGUF", "inputs": {"unet_name": "QwenEdit\\qwen-image-edit-2511-Q4_K_S.gguf"}},
        "2": {"class_type": "LoraLoaderModelOnly", "inputs": {"model": ["1", 0], "strength_model": 1.0,
              "lora_name": "QwenEdit\\accel\\Qwen-Image-Edit-2511-Lightning-4steps-V1.0-bf16.safetensors"}},
        "3": {"class_type": "ModelSamplingAuraFlow", "inputs": {"model": ["2", 0], "shift": 3.1}},
        "4": {"class_type": "CFGNorm", "inputs": {"model": ["3", 0], "strength": 1.0}},
        "5": {"class_type": "CLIPLoader", "inputs": {"clip_name": "Qwen\\qwen_2.5_vl_7b_fp8_scaled.safetensors",
                                                     "type": "qwen_image", "device": "cpu"}},
        "6": {"class_type": "VAELoader", "inputs": {"vae_name": "QwenEdit\\qwen_image_vae.safetensors"}},
        "7": {"class_type": "LoadImage", "inputs": {"image": img1}},
        "8": {"class_type": "ImageScaleToTotalPixels", "inputs": {"image": ["7", 0], "upscale_method": "lanczos",
                                                                  "megapixels": megapixels, "resolution_steps": 16}},
        "11": {"class_type": "TextEncodeQwenImageEditPlus", "inputs": {"clip": ["5", 0], "prompt": prompt, "vae": ["6", 0], "image1": ["8", 0]}},
        "12": {"class_type": "TextEncodeQwenImageEditPlus", "inputs": {"clip": ["5", 0], "prompt": NEG, "vae": ["6", 0], "image1": ["8", 0]}},
        "13": {"class_type": "VAEEncode", "inputs": {"pixels": ["8", 0], "vae": ["6", 0]}},
        "14": {"class_type": "KSampler", "inputs": {"model": ["4", 0], "seed": seed, "steps": 4, "cfg": 1.0,
                                                    "sampler_name": "euler", "scheduler": "simple", "positive": ["11", 0],
                                                    "negative": ["12", 0], "latent_image": ["13", 0], "denoise": 1.0}},
        "15": {"class_type": "VAEDecode", "inputs": {"samples": ["14", 0], "vae": ["6", 0]}},
        "16": {"class_type": "SaveImage", "inputs": {"images": ["15", 0], "filename_prefix": "agent_sprites/q"}},
    }
    if img2:
        g["9"] = {"class_type": "LoadImage", "inputs": {"image": img2}}
        g["10"] = {"class_type": "ImageScaleToTotalPixels", "inputs": {"image": ["9", 0], "upscale_method": "lanczos",
                                                                       "megapixels": megapixels, "resolution_steps": 16}}
        for k in ("11", "12"):
            g[k]["inputs"]["image2"] = ["10", 0]
    return g


def run(job):
    img1 = upload(job["image"])
    img2 = upload(job["image2"]) if job.get("image2") else None
    pid = json.loads(urllib.request.urlopen(urllib.request.Request(
        f"{SERVER}/prompt", data=json.dumps({"prompt": graph(img1, job["prompt"], job.get("seed", 7), img2,
                                                              job.get("megapixels", 1.0)),
                                             "client_id": "agent-sprites"}).encode(),
        headers={"Content-Type": "application/json"})).read())["prompt_id"]
    t0 = time.time()
    while True:
        time.sleep(1.5)
        h = json.loads(urllib.request.urlopen(f"{SERVER}/history/{pid}").read())
        if pid in h:
            st = h[pid].get("status", {})
            if st.get("status_str") == "error":
                return job["name"], False, json.dumps(st.get("messages", []))[-400:]
            outs = [im for o in h[pid]["outputs"].values() for im in o.get("images", [])]
            if outs:
                im = outs[0]
                q = urllib.parse.urlencode({"filename": im["filename"], "subfolder": im["subfolder"], "type": im["type"]})
                data = urllib.request.urlopen(f"{SERVER}/view?{q}").read()
                os.makedirs(job["out"], exist_ok=True)
                dst = os.path.join(job["out"], job["name"] + ".png")
                open(dst, "wb").write(data)
                return job["name"], True, f"{dst} ({time.time() - t0:.0f}s)"
        if time.time() - t0 > 900:
            return job["name"], False, "timeout"


def main():
    global SERVER
    if "--server" in sys.argv:
        SERVER = sys.argv[sys.argv.index("--server") + 1]
    jobs = json.load(open(sys.argv[1], encoding="utf-8"))
    for job in jobs:  # the GPU runs one at a time; queueing them all at once gains nothing
        name, ok, info = run(job)
        print(f"{name}\t{'ok' if ok else 'FAIL'}\t{info}", flush=True)


if __name__ == "__main__":
    main()
