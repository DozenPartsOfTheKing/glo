#!/usr/bin/env python3
"""Glo icon: draws the tile, builds glo.ico and the src-go/icon.syso resource.

Run whenever the artwork changes:  python3 icon/make_icon.py
Needs Pillow. The result (glo.ico, icon.syso, glo.png) lives in the repo,
so this script isn't needed for a regular exe build.
"""
import math, os, struct, io
from PIL import Image, ImageDraw, ImageFilter, ImageFont

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)

# ----------------------------------------------------------------- drawing

CORE = (236, 250, 255)   # letter core — nearly white
NEON = (120, 205, 255)   # glow and underline
FONT = "/System/Library/Fonts/Avenir Next.ttc"

def squircle(size, n=4.2, inset=0.0):
    """Superellipse |x|^n+|y|^n=1 — the tile's shape, corners rounder than a plain rounded rect."""
    m = Image.new("L", (size, size), 0)
    px = m.load()
    r, a = size / 2, size / 2 - inset
    for y in range(size):
        fy = (y + 0.5 - r) / a
        if abs(fy) > 1:
            continue
        t = 1 - abs(fy) ** n
        if t <= 0:
            continue
        fx = t ** (1 / n)
        x0, x1 = r - fx * a, r + fx * a
        for x in range(max(0, int(math.floor(x0))), min(size, int(math.ceil(x1)))):
            cov = min(x + 1, x1) - max(x, x0)          # fractional coverage = anti-aliasing
            px[x, y] = max(px[x, y], int(255 * max(0.0, min(1.0, cov))))
    return m

def vgrad(size, top, bot):
    g = Image.new("RGB", (1, size))
    d = g.load()
    for y in range(size):
        t = y / (size - 1)
        d[0, y] = tuple(int(top[i] + (bot[i] - top[i]) * t) for i in range(3))
    return g.resize((size, size), Image.BILINEAR)

def tile(size, simple):
    """Black glossy glass. simple=True — version for 16-32 px: without
    fine details that turn into mud at that size."""
    outer = squircle(size)
    inner = squircle(size, inset=size * (0.030 if simple else 0.052))
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    img.paste(vgrad(size, (46, 48, 53), (12, 13, 15)), (0, 0), outer)   # body
    img.paste(vgrad(size, (26, 28, 32), (5, 5, 7)), (0, 0), inner)      # lens

    sheen = Image.new("L", (size, size), 0)                              # gloss on top
    ImageDraw.Draw(sheen).ellipse([-size*0.30, -size*0.55, size*1.30, size*0.30], fill=95)
    sheen = Image.composite(sheen.filter(ImageFilter.GaussianBlur(size*0.05)),
                            Image.new("L", (size, size), 0), inner)
    img.paste(Image.new("RGB", (size, size), (226, 236, 255)), (0, 0), sheen)

    if not simple:                                                       # window reflection
        sp = Image.new("L", (size, size), 0)
        ImageDraw.Draw(sp).rounded_rectangle(
            [size*0.165, size*0.30, size*0.285, size*0.365], radius=size*0.018, fill=105)
        sp = Image.composite(sp.filter(ImageFilter.GaussianBlur(size*0.010)),
                             Image.new("L", (size, size), 0), inner)
        img.paste(Image.new("RGB", (size, size), (255, 255, 255)), (0, 0), sp)

    ring = Image.composite(Image.new("L", (size, size), 255),             # rim
                           Image.new("L", (size, size), 0), outer)
    ring = Image.composite(Image.new("L", (size, size), 0), ring,
                           squircle(size, inset=max(1.2, size*(0.010 if simple else 0.014))))
    img.paste(vgrad(size, (215, 222, 235), (52, 54, 60)), (0, 0),
              ring.filter(ImageFilter.GaussianBlur(size*0.003)))

    if not simple:                                                        # groove around the lens
        gr = Image.composite(Image.new("L", (size, size), 120),
                             Image.new("L", (size, size), 0), inner)
        gr = Image.composite(Image.new("L", (size, size), 0), gr,
                             squircle(size, inset=size*0.060))
        img.paste(Image.new("RGB", (size, size), (0, 0, 0)), (0, 0),
                  gr.filter(ImageFilter.GaussianBlur(size*0.004)))
    return img, outer

def glow(mask, color, radius, mul):
    g = Image.eval(mask.filter(ImageFilter.GaussianBlur(radius)),
                   lambda v: min(255, int(v * mul)))
    c = Image.new("RGBA", mask.size, color + (0,))
    c.putalpha(g)
    return c

def letter(size, ch, scale, dy=0.0):
    m = Image.new("L", (size, size), 0)
    d = ImageDraw.Draw(m)
    f = ImageFont.truetype(FONT, int(size * scale), index=0)
    b = d.textbbox((0, 0), ch, font=f)
    w, h = b[2] - b[0], b[3] - b[1]
    d.text(((size - w) / 2 - b[0], (size - h) / 2 - b[1] + size * dy), ch, font=f, fill=255)
    return m

def bar(size, y, w, lw):
    m = Image.new("L", (size, size), 0)
    x0 = size * (0.5 - w / 2)
    ImageDraw.Draw(m).rounded_rectangle(
        [x0, size*y - size*lw/2, x0 + size*w, size*y + size*lw/2], radius=size*lw/2, fill=255)
    return m

def render(size, simple=False):
    img, outer = tile(size, simple)
    marks = [(letter(size, "G", 0.56 if simple else 0.50, -0.055), CORE),
             (bar(size, 0.76 if simple else 0.745, 0.34, 0.052), NEON)]
    for m, _ in marks:                                   # wide soft halo
        img.alpha_composite(glow(m, NEON, size*0.055, 1.0))
    for m, c in marks:                                   # tight bright outline
        img.alpha_composite(glow(m, c, size*0.018, 1.9))
    for m, c in marks:                                   # the core itself
        core = Image.new("RGBA", (size, size), c + (0,))
        core.putalpha(m)
        img.alpha_composite(core)
    a = img.getchannel("A")                              # nothing beyond the tile
    img.putalpha(Image.composite(a, Image.new("L", (size, size), 0), outer))
    return img

# --------------------------------------------------------------------- .ico

ICO_SIZES = [16, 24, 32, 48, 64, 128, 256]

def ico_images():
    big = render(1024, simple=False)
    small = render(512, simple=True)
    out = []
    for s in ICO_SIZES:
        src = small if s <= 32 else big
        out.append((s, src.resize((s, s), Image.LANCZOS)))
    return out

def bmp_entry(im):
    """Classic .ico entry: BITMAPINFOHEADER + bottom-up BGRA + AND mask."""
    w, h = im.size
    px = im.load()
    hdr = struct.pack("<IiiHHIIiiII", 40, w, h * 2, 1, 32, 0, w * h * 4, 0, 0, 0, 0)
    xor = bytearray()
    for y in range(h - 1, -1, -1):
        for x in range(w):
            r, g, b, a = px[x, y]
            xor += bytes((b, g, r, a))
    stride = ((w + 31) // 32) * 4                 # 1 bit per pixel, row padded to a multiple of 4
    mask = bytearray()
    for y in range(h - 1, -1, -1):
        row = bytearray(stride)
        for x in range(w):
            if px[x, y][3] < 128:
                row[x // 8] |= 0x80 >> (x % 8)
        mask += row
    return bytes(hdr) + bytes(xor) + bytes(mask)

def png_entry(im):
    buf = io.BytesIO()
    im.save(buf, format="PNG")
    return buf.getvalue()

def build_ico(images):
    blobs = [(s, png_entry(im) if s >= 256 else bmp_entry(im)) for s, im in images]
    head = struct.pack("<HHH", 0, 1, len(blobs))
    off = len(head) + 16 * len(blobs)
    dirs = b""
    for s, data in blobs:
        b = 0 if s >= 256 else s
        dirs += struct.pack("<BBBBHHII", b, b, 0, 0, 1, 32, len(data), off)
        off += len(data)
    return head + dirs + b"".join(d for _, d in blobs), blobs

# -------------------------------------------------------------------- .syso
# COFF object with a single .rsrc section: a resource tree (type -> id -> lang),
# data entries, and the images themselves. Each data entry's OffsetToData field
# must become an RVA, so an ADDR32NB relocation to the section symbol is placed
# on it: the Go linker substitutes the section's address and adds the offset
# stored in the field to it.

RT_ICON, RT_GROUP_ICON, LANG = 3, 14, 1033

def align(n, a=8):
    return (n + a - 1) // a * a

def build_syso(blobs):
    n = len(blobs)
    group = struct.pack("<HHH", 0, 1, n)
    for i, (s, data) in enumerate(blobs, start=1):
        b = 0 if s >= 256 else s
        group += struct.pack("<BBBBHHIH", b, b, 0, 0, 1, 32, len(data), i)

    res = [(RT_ICON, i, data) for i, (_, data) in enumerate(blobs, start=1)]
    res.append((RT_GROUP_ICON, 1, group))

    root_sz = 16 + 2 * 8
    icon_dir_sz = 16 + n * 8
    grp_dir_sz = 16 + 8
    lang_sz = 16 + 8

    off_icon_dir = root_sz
    off_grp_dir = off_icon_dir + icon_dir_sz
    off_langs = off_grp_dir + grp_dir_sz
    off_entries = off_langs + lang_sz * len(res)
    off_data = align(off_entries + 16 * len(res))

    data_off, blob_area = [], b""
    for _, _, data in res:
        data_off.append(off_data + len(blob_area))
        blob_area += data + b"\x00" * (align(len(data)) - len(data))

    def rdir(named, ids):
        return struct.pack("<IIHHHH", 0, 0, 0, 0, named, ids)

    buf = bytearray()
    buf += rdir(0, 2)
    buf += struct.pack("<II", RT_ICON, 0x80000000 | off_icon_dir)
    buf += struct.pack("<II", RT_GROUP_ICON, 0x80000000 | off_grp_dir)

    buf += rdir(0, n)                                   # RT_ICON: id 1..n
    for i in range(n):
        buf += struct.pack("<II", i + 1, 0x80000000 | (off_langs + lang_sz * i))
    buf += rdir(0, 1)                                   # RT_GROUP_ICON: id 1
    buf += struct.pack("<II", 1, 0x80000000 | (off_langs + lang_sz * n))

    for i in range(len(res)):                           # language level
        buf += rdir(0, 1)
        buf += struct.pack("<II", LANG, off_entries + 16 * i)

    relocs = []
    for i, (_, _, data) in enumerate(res):
        relocs.append(len(buf))                         # the OffsetToData field
        buf += struct.pack("<IIII", data_off[i], len(data), 0, 0)
    buf += b"\x00" * (off_data - len(buf))
    buf += blob_area

    section = bytes(buf)
    ptr_raw = 20 + 40
    ptr_rel = ptr_raw + len(section)
    ptr_sym = ptr_rel + 10 * len(relocs)

    out = bytearray()
    out += struct.pack("<HHIIIHH", 0x8664, 1, 0, ptr_sym, 2, 0, 0)
    out += b".rsrc\0\0\0" + struct.pack("<IIIIIIHHI", 0, 0, len(section), ptr_raw,
                                         ptr_rel, 0, len(relocs), 0, 0x40000040)
    out += section
    for r in relocs:
        out += struct.pack("<IIH", r, 0, 3)             # symbol 0, IMAGE_REL_AMD64_ADDR32NB
    out += b".rsrc\0\0\0" + struct.pack("<IhHBB", 0, 1, 0, 3, 1)   # section symbol + aux
    out += struct.pack("<IHHIHBBBB", len(section), len(relocs), 0, 0, 0, 0, 0, 0, 0)
    out += struct.pack("<I", 4)                          # empty string table
    return bytes(out)

# --------------------------------------------------------------------- main

if __name__ == "__main__":
    images = ico_images()
    ico, blobs = build_ico(images)
    open(os.path.join(HERE, "glo.ico"), "wb").write(ico)
    open(os.path.join(ROOT, "src-go", "icon.syso"), "wb").write(build_syso(blobs))
    render(512, simple=False).save(os.path.join(HERE, "glo.png"))
    print("glo.ico: %d bytes, %d sizes" % (len(ico), len(blobs)))
