"""Assembles frames/*.png (in filename order) into demo.gif."""
import glob
from PIL import Image

paths = sorted(glob.glob("frames/*.png"))
frames = [Image.open(p).convert("RGB") for p in paths]
# Idle/done frames held a bit longer than progress frames.
durations = []
for p in paths:
    if "idle" in p or "done" in p or "start" in p:
        durations.append(1400)
    else:
        durations.append(700)

frames[0].save(
    "demo.gif",
    save_all=True,
    append_images=frames[1:],
    duration=durations,
    loop=0,
)
print(f"wrote demo.gif from {len(frames)} frames")
