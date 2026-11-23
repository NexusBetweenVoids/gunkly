#!/usr/bin/env python3
"""Shrink screenshots: 256-colour palette (like the originals), then optipng if present.

Usage: python3 scripts/shrink-png.py ../docs/assets/screens/*.png
Optional dev tool; needs Pillow. Without it the files are left as they are.
"""
import shutil
import subprocess
import sys

try:
    from PIL import Image
except ImportError:
    print("shrink-png: Pillow not installed, screenshots left uncompressed")
    sys.exit(0)

for path in sys.argv[1:]:
    im = Image.open(path).convert("RGB")
    im.quantize(colors=256, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE).save(path, optimize=True)
    if shutil.which("optipng"):
        subprocess.run(["optipng", "-quiet", "-o2", path], check=True)
    print(f"shrink-png: {path}")
