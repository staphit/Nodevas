"""Generate images through agy's image-generator subagent (Gemini), no API key.

    python gen.py jobs.json            # runs every job in parallel (max --par)

jobs.json: [{"name": "deepseek_door_a", "refs": ["path.png", ...], "prompt": "...", "out": "dir"}]
Each job runs agy in its own temp folder holding copies of the refs, then copies
the generated file (agy saves it in its brain folder as <name>_<ts>.<ext>) to
<out>/<name>.<ext>. Prints one line per job: name, ok/FAIL, path or log tail.
"""
import concurrent.futures as cf
import glob
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time

AGY = r"C:/Users/g2772/AppData/Local/agy/bin/agy.exe"
BRAIN = os.path.join(os.environ["USERPROFILE"], ".gemini", "antigravity-cli", "brain")


def run(job):
    name, out = job["name"], job["out"]
    os.makedirs(out, exist_ok=True)
    work = tempfile.mkdtemp(prefix=f"agy-{name}-")
    refs = []
    for i, r in enumerate(job.get("refs", [])):
        dst = os.path.join(work, f"ref{i + 1}{os.path.splitext(r)[1]}")
        shutil.copy(r, dst)
        refs.append(os.path.basename(dst))
    ref_text = ""
    if refs:
        ref_text = (" Give the generator these reference images from this folder: "
                    + ", ".join(refs) + ". " + job.get("ref_roles", ""))
    prompt = (f"Task: use the image-generator subagent to make ONE image named {name}."
              f"{ref_text} Image request: {job['prompt']} When done reply with only the saved file path.")
    start = time.time() - 5
    for attempt in range(job.get("retries", 2) + 1):
        try:
            p = subprocess.run([AGY, "--print-timeout", "560s", "-p", prompt], cwd=work,
                               capture_output=True, text=True, encoding="utf-8", errors="replace", timeout=600)
            log = (p.stdout or "") + (p.stderr or "")
        except subprocess.TimeoutExpired:
            log = "timeout"
        hits = [f for f in glob.glob(os.path.join(BRAIN, "*", f"{name}_*"))
                if os.path.getmtime(f) >= start and os.path.splitext(f)[1].lower() in (".png", ".jpg", ".jpeg", ".webp")]
        if hits:
            src = max(hits, key=os.path.getmtime)
            dst = os.path.join(out, name + os.path.splitext(src)[1].lower())
            shutil.copy(src, dst)
            shutil.rmtree(work, ignore_errors=True)
            return name, True, dst
    shutil.rmtree(work, ignore_errors=True)
    return name, False, log[-400:].replace("\n", " ")


def main():
    jobs = json.load(open(sys.argv[1], encoding="utf-8"))
    par = int(sys.argv[2]) if len(sys.argv) > 2 else 6
    with cf.ThreadPoolExecutor(par) as ex:
        for name, ok, info in ex.map(run, jobs):
            print(f"{name}\t{'ok' if ok else 'FAIL'}\t{info}", flush=True)


if __name__ == "__main__":
    main()
