#!/usr/bin/env python3
"""Membuat ikon aplikasi Belgindo Office Messenger (PNG + .ico multi-ukuran).

Hasil:
  assets/app_icon.png        1024x1024 (master)
  assets/icon.ico            16/24/32/48/64/128/256 (ikon .exe)
  assets/tray_normal.png     32x32 (tray)
  assets/tray_alert.png      32x32 (tray, ada pesan belum dibaca)
  internal/ui/assets/app_icon_win.png  48x48 (ikon jendela/taskbar, di-embed Go)
  internal/ui/assets/tray_normal.png / tray_alert.png  disalin ke paket ui

Jalankan:  python tools/make_icon.py
"""
import os
import subprocess
from PIL import Image, ImageDraw, ImageFilter

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
ASSETS = os.path.join(ROOT, "assets")
UI_ASSETS = os.path.join(ROOT, "internal", "ui", "assets")

S = 4096  # kanvas besar, nanti di-downscale supaya tepi halus


def lerp(a, b, t):
    return tuple(int(round(a[i] + (b[i] - a[i]) * t)) for i in range(3))


NAVY = (31, 78, 121)
AZURE = (46, 168, 224)
GREEN = (34, 197, 94)


def rounded_mask(size, radius):
    m = Image.new("L", (size, size), 0)
    d = ImageDraw.Draw(m)
    d.rounded_rectangle([0, 0, size - 1, size - 1], radius=radius, fill=255)
    return m


def gradient_bg(size):
    base = Image.new("RGB", (size, size))
    px = base.load()
    for y in range(size):
        t = y / (size - 1)
        c = lerp(NAVY, AZURE, t)
        for x in range(size):
            px[x, y] = c
    return base


def draw_master():
    img = gradient_bg(S).convert("RGBA")

    # --- bubble ---
    pad = int(S * 0.19)
    bx0, by0, bx1, by1 = pad, int(S * 0.20), S - pad, int(S * 0.68)
    w_tail = int(S * 0.11)

    bub = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    bd = ImageDraw.Draw(bub)
    r = int((bx1 - bx0) * 0.26)
    # ekor pendek & gemuk agar tetap terbaca pada 16px
    tx = bx0 + int((bx1 - bx0) * 0.22)
    bd.polygon(
        [(tx + int(w_tail), by1 - 2), (bx0 + int(S * 0.055), by1 + int(S * 0.085)),
         (tx - int(w_tail * 0.35), by1 - 2)],
        fill=(255, 255, 255, 255),
    )
    bd.rounded_rectangle([bx0, by0, bx1, by1], radius=r, fill=(255, 255, 255, 255))

    # --- jaringan peer-to-peer di dalam bubble ---
    cx = (bx0 + bx1) / 2
    cy = (by0 + by1) / 2
    w = bx1 - bx0
    left = (cx - w * 0.26, cy + w * 0.06)
    right = (cx + w * 0.26, cy + w * 0.06)
    top = (cx, cy - w * 0.14)
    node_r = int(w * 0.075)
    link = int(w * 0.028)

    net = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    nd = ImageDraw.Draw(net)
    for p in (top, left, right):
        nd.line([p, (cx, cy + w * 0.02)], fill=(46, 116, 168, 255), width=link)

    def node(p, color):
        x, y = p
        nd.ellipse([x - node_r, y - node_r, x + node_r, y + node_r], fill=color)

    node(left, (31, 78, 121, 255))
    node(right, (31, 78, 121, 255))
    node(top, (46, 168, 224, 255))

    # --- titik status hijau di pojok kanan atas bubble ---
    dot_r = int(w * 0.085)
    dx, dy = bx1 - int(w * 0.06), by0 + int(w * 0.06)
    ring = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    rd = ImageDraw.Draw(ring)
    rd.ellipse([dx - dot_r, dy - dot_r, dx + dot_r, dy + dot_r],
               fill=(255, 255, 255, 255))
    rd.ellipse([dx - dot_r * 0.72, dy - dot_r * 0.72, dx + dot_r * 0.72, dy + dot_r * 0.72],
               fill=GREEN + (255,))

    # --- bayangan lembut di bawah bubble ---
    sh = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    sd = ImageDraw.Draw(sh)
    sd.rounded_rectangle([bx0 + 20, by0 + int(S * 0.035), bx1 + 20, by1 + int(S * 0.035)],
                         radius=r, fill=(0, 0, 0, 130))
    sh = sh.filter(ImageFilter.GaussianBlur(S * 0.012))

    out = sh
    out = Image.alpha_composite(out, bub)
    out = Image.alpha_composite(out, net)
    out = Image.alpha_composite(out, ring)
    out = Image.alpha_composite(img, out)

    # --- potong ke bentuk rounded square ---
    mask = rounded_mask(S, int(S * 0.225))
    final = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    final.paste(out.resize((S, S)), (0, 0), mask)

    # garis tepi tipis biar tetap terbaca di taskbar gelap
    edge = ImageDraw.Draw(final)
    er = int(S * 0.225)
    edge.rounded_rectangle([2, 2, S - 3, S - 3], radius=er,
                           outline=(255, 255, 255, 70), width=max(2, S // 256))
    return final


def tray_variant(master, alert):
    """Ikon tray: bubble + titik status (hijau / oranye saat ada pesan)."""
    size = 128
    im = master.resize((size, size), Image.LANCZOS)
    d = ImageDraw.Draw(im)
    color = (245, 158, 11, 255) if alert else (34, 197, 94, 255)
    r = int(size * 0.17)
    cx, cy = int(size * 0.80), int(size * 0.22)
    d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=(255, 255, 255, 255))
    rr = int(r * 0.72)
    d.ellipse([cx - rr, cy - rr, cx + rr, cy + rr], fill=color)
    if alert:
        n = 3
        dr = int(size * 0.045)
        for i in range(n):
            x = int(size * 0.20) + i * int(size * 0.11)
            d.ellipse([x - dr, cy - dr, x + dr, cy + dr], fill=(255, 255, 255, 255))
    return im


def main():
    os.makedirs(ASSETS, exist_ok=True)
    os.makedirs(UI_ASSETS, exist_ok=True)

    master = draw_master()
    master_1024 = master.resize((1024, 1024), Image.LANCZOS)
    master_1024.save(os.path.join(ASSETS, "app_icon.png"))

    ico_sizes = [(16, 16), (20, 20), (24, 24), (32, 32), (40, 40), (48, 48),
                 (64, 64), (128, 128), (256, 256)]
    master_1024.save(os.path.join(ASSETS, "icon.ico"), format="ICO", sizes=ico_sizes)

    tray_variant(master, False).resize((32, 32), Image.LANCZOS).save(
        os.path.join(ASSETS, "tray_normal.png"))
    tray_variant(master, True).resize((32, 32), Image.LANCZOS).save(
        os.path.join(ASSETS, "tray_alert.png"))

    for name in ("tray_normal.png", "tray_alert.png"):
        src = os.path.join(ASSETS, name)
        dst = os.path.join(UI_ASSETS, name)
        with open(src, "rb") as f:
            data = f.read()
        with open(dst, "wb") as f:
            f.write(data)

    master_1024.resize((48, 48), Image.LANCZOS).save(
        os.path.join(UI_ASSETS, "app_icon_win.png"))

    print("ikon dibuat di", ASSETS)
    subprocess.run(["python", "-c",
                    "from PIL import Image;print('ico ok', Image.open(r'%s').size)" %
                    os.path.join(ASSETS, "icon.ico")])


if __name__ == "__main__":
    main()
