# Anime war-room sprites

The anime character sheets in `web/src/game/anime/sheets/` (one per character:
GPT, Claude, DeepSeek, Gemini; 18 actions × 8 frames, 192 px) come from here.
The contract they keep is in `web/src/game/anime/manifest.ts`.

1. **Designs.** One chibi drawing per character, made with Gemini through
   `agy`'s image tool (`gen.py`), on a flat green background.
2. **Base drawings.** Local ComfyUI, Qwen-Image-Edit-2511 with the 4-step
   Lightning LoRA (`qedit.py`): from each design, a rig-friendly A-pose front
   and side (`mv/`), seated from the front, riding her bike (`sit/`), the
   sleeping, sad and happy faces of the seated one, and copies without the
   moving limbs (`*_noarm`) that paint what an arm or leg covered. Props:
   laptop, backpack (`props/`); the desk, rod, tongs and pole are drawn in code.
3. **Rigs.** `rig/specs/<character>_<base>.json` says where the joints are and
   how Segment Anything finds each part (`sam_masks.py`, run with ComfyUI's
   Python). `rigkit.py` cuts the drawing into layers (limbs split at the elbow
   and knee), `rig2d.py` turns them about their joints, `actions2d.py` poses
   every action, two-bone IK for hands and feet.
4. **Sheets.** `python make_rig.py <character> [action ...]` renders the
   actions into `$SPRITE_OUT/<character>/` (hi 384 px frames, 192 px frames,
   `<character>.webp` + `.json`); copy the pair into the app's `sheets/`.
   `python make_rig.py <character> --check <base>` shows a spec's layers,
   joints and rest pose; `cast_gif.py` makes an animated grid of all actions.

The generated drawings are not in git (see `.gitignore`).
