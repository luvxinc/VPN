#!/usr/bin/env python3
"""生成 为爱鼓掌 App 图标（红色心形，透明背景）"""

import math, os, subprocess
from PIL import Image, ImageDraw, ImageFilter

def heart_points(size, padding_ratio=0.10):
    pad = int(size * padding_ratio)
    w   = size - 2 * pad
    h   = size - 2 * pad

    raw = []
    for i in range(1440):
        t = math.radians(i / 4)
        x =  16 * math.sin(t) ** 3
        y = -(13 * math.cos(t) - 5 * math.cos(2*t)
              - 2 * math.cos(3*t) - math.cos(4*t))
        raw.append((x, y))

    min_x = min(p[0] for p in raw); max_x = max(p[0] for p in raw)
    min_y = min(p[1] for p in raw); max_y = max(p[1] for p in raw)

    return [(pad + (x - min_x) / (max_x - min_x) * w,
             pad + (y - min_y) / (max_y - min_y) * h) for x, y in raw]


def make_heart(size):
    img  = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    pts  = heart_points(size)

    # 1. 柔和阴影
    shadow = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    ImageDraw.Draw(shadow).polygon(
        [(x + size * 0.025, y + size * 0.035) for x, y in pts],
        fill=(0, 0, 0, 55)
    )
    img = Image.alpha_composite(img, shadow.filter(ImageFilter.GaussianBlur(size * 0.04)))

    # 2. 渐变心形（由深红到亮红，从下到上）
    base   = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    draw   = ImageDraw.Draw(base)
    steps  = 40
    for s in range(steps, 0, -1):
        f   = s / steps                          # 1.0 (底) → 接近0 (顶)
        r   = int(200 + 45 * f)                  # 245 → 200
        g   = int(15  + 25 * (1 - f))            # 15  → 40
        b   = int(35  + 15 * (1 - f))            # 35  → 50
        off = s * 0.25
        draw.polygon([(x, y + off) for x, y in pts], fill=(r, g, b, 255))
    draw.polygon(pts, fill=(235, 35, 55, 255))
    img = Image.alpha_composite(img, base)

    # 3. 左上高光（玻璃感）
    hl = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    r  = int(size * 0.22)
    cx = int(size * 0.37); cy = int(size * 0.28)
    ImageDraw.Draw(hl).ellipse([cx - r, cy - r, cx + r, cy + r], fill=(255, 255, 255, 70))
    img = Image.alpha_composite(img, hl.filter(ImageFilter.GaussianBlur(size * 0.07)))

    return img


def build_iconset(base: Image.Image, out_dir: str):
    os.makedirs(out_dir, exist_ok=True)
    specs = [
        ("icon_16x16.png",       16),
        ("icon_16x16@2x.png",    32),
        ("icon_32x32.png",       32),
        ("icon_32x32@2x.png",    64),
        ("icon_128x128.png",     128),
        ("icon_128x128@2x.png",  256),
        ("icon_256x256.png",     256),
        ("icon_256x256@2x.png",  512),
        ("icon_512x512.png",     512),
        ("icon_512x512@2x.png",  1024),
    ]
    for name, s in specs:
        base.resize((s, s), Image.LANCZOS).save(os.path.join(out_dir, name))
        print(f"  {s:4d}px  {name}")


if __name__ == "__main__":
    print("生成心形图标...")
    icon = make_heart(1024)

    iconset_dir = "Resources/AppIcon.iconset"
    build_iconset(icon, iconset_dir)

    # 用 iconutil 打包成 .icns
    icns_path = "Resources/AppIcon.icns"
    subprocess.run(["iconutil", "-c", "icns", iconset_dir, "-o", icns_path], check=True)
    print(f"\n✓ 图标生成完成: {icns_path}")

    # 预览（可选）
    icon.save("Resources/preview_1024.png")
    subprocess.run(["open", "Resources/preview_1024.png"])
