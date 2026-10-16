#!/usr/bin/env python3
"""Render the missing "m" thumbs of the .tgs files under data/sticker-seed.

Every real Telegram .tgs document carries a small raster thumb (photoSize
"m", a 128x128 webp of the animation's first frame); clients show it whenever
they draw the sticker frozen, and without it they download the .tgs itself
into an <img>. Some exports (custom emoji, effects, GiftsPremium, Topics)
came without those files. This writes one next to every .tgs that lacks it,
named the way the exporter names them, so the seeder picks it up
(internal/app/files/seed.go, seedRasterFallbackThumb):

    <name>_thumb1_PhotoSize_typem_128x128.jpg   (webp bytes, like the export)

Reactions are skipped: they use their own static_icon instead.

Needs rlottie-python and Pillow:  pip install rlottie-python pillow

    python scripts/render-seed-thumbs.py [data/sticker-seed]
"""

import os
import re
import sys

from rlottie_python import LottieAnimation

SIZE = 128
SKIP_DIRS = {"telegram_reactions_export"}
THUMB_MARKER = re.compile(r"_thumb\d+_PhotoSize_typem")


def frame_to_render(anim):
    """Frame 0, as Telegram uses -- unless it is blank (an animation that
    fades in), then the most covered of a few sampled frames."""
    total = anim.lottie_animation_get_totalframe()
    best, best_cover = 0, -1
    for frame in [0] + [total * k // 8 for k in range(1, 8)]:
        alpha = anim.render_pillow_frame(frame_num=frame, width=SIZE, height=SIZE).getchannel("A")
        cover = sum(1 for a in alpha.getdata() if a)
        if frame == 0 and cover > SIZE * SIZE // 50:
            return 0
        if cover > best_cover:
            best, best_cover = frame, cover
    return best


def main():
    root = sys.argv[1] if len(sys.argv) > 1 else os.path.join("data", "sticker-seed")
    made = skipped = failed = 0
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        have = {THUMB_MARKER.split(f)[0] for f in filenames if THUMB_MARKER.search(f)}
        for name in sorted(filenames):
            if not name.endswith(".tgs"):
                continue
            base = name[: -len(".tgs")]
            if base in have:
                skipped += 1
                continue
            path = os.path.join(dirpath, name)
            try:
                anim = LottieAnimation.from_tgs(path)
                image = anim.render_pillow_frame(frame_num=frame_to_render(anim), width=SIZE, height=SIZE)
            except Exception as err:  # a broken file should not stop the rest
                print(f"skip {path}: {err}", file=sys.stderr)
                failed += 1
                continue
            out = os.path.join(dirpath, f"{base}_thumb1_PhotoSize_typem_{SIZE}x{SIZE}.jpg")
            image.save(out, "WEBP", quality=80, method=6)
            made += 1
    print(f"rendered {made}, already had one {skipped}, failed {failed}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
