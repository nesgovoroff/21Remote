# SPDX-License-Identifier: AGPL-3.0-or-later
"""Draws the rack pictures of web/img (cards, power supply, fader caps).

Run: python3 tools/art/cards.py   (needs Pillow and the DejaVu fonts)
All pictures are drawn here from scratch.
"""
import os
from PIL import Image, ImageDraw, ImageFont, ImageFilter

S = 4  # drawn at 4x, scaled down for smooth edges
H = 297
OUT = os.path.join(os.path.dirname(__file__), '..', '..', 'web', 'img')
FONT_B = '/usr/share/fonts/truetype/dejavu/DejaVuSansCondensed-Bold.ttf'
FONT = '/usr/share/fonts/truetype/dejavu/DejaVuSansCondensed.ttf'


def f(v):
    return int(round(v * S))


def font(size, bold=True):
    return ImageFont.truetype(FONT_B if bold else FONT, f(size))


class Card:
    def __init__(self, w):
        self.w = w
        self.im = Image.new('RGBA', (f(w), f(H)), (0, 0, 0, 0))
        self.d = ImageDraw.Draw(self.im)
        self.panel()

    # brushed aluminium face with bevelled edges
    def panel(self):
        w, d = self.w, self.d
        for x in range(f(w)):
            t = x / f(w)
            base = 222 - 14 * abs(t - 0.35)
            d.line((x, 0, x, f(H)), fill=(int(base), int(base + 2), int(base + 5), 255))
        for y in range(0, f(H), f(1.5)):
            d.line((0, y, f(w), y), fill=(255, 255, 255, 10))
        d.rectangle((0, 0, f(w) - 1, f(H) - 1), outline=(120, 124, 128, 255), width=f(0.8))
        d.line((f(0.8), f(0.8), f(0.8), f(H) - f(1)), fill=(250, 251, 252, 255), width=f(0.6))
        d.line((f(w) - f(1.4), 0, f(w) - f(1.4), f(H)), fill=(150, 154, 158, 255), width=f(0.6))
        # captive screw at the top; the bottom strip carries the slot number (drawn by the app)
        self.screw(w / 2, 7)
        # pull handle
        hy = H - 34
        d.rounded_rectangle((f(3), f(hy), f(w - 3), f(hy + 18)), radius=f(3), fill=(182, 186, 190, 255),
                            outline=(110, 114, 118, 255), width=f(0.7))
        d.rounded_rectangle((f(5), f(hy + 3), f(w - 5), f(hy + 15)), radius=f(2), fill=(52, 56, 60, 255))
        for i in range(3):
            yy = hy + 6 + i * 3
            d.line((f(8), f(yy), f(w - 8), f(yy)), fill=(96, 100, 104, 255), width=f(0.8))

    def screw(self, cx, cy, r=3.2):
        d = self.d
        d.ellipse((f(cx - r), f(cy - r), f(cx + r), f(cy + r)), fill=(160, 164, 168, 255), outline=(80, 84, 88, 255), width=f(0.6))
        d.ellipse((f(cx - r + 0.9), f(cy - r + 0.9), f(cx + r - 0.9), f(cy + r - 0.9)), fill=(196, 200, 204, 255))
        d.line((f(cx - r + 1.2), f(cy), f(cx + r - 1.2), f(cy)), fill=(60, 64, 68, 255), width=f(1))
        d.line((f(cx), f(cy - r + 1.2), f(cx), f(cy + r - 1.2)), fill=(60, 64, 68, 255), width=f(1))

    def title(self, lines, y=16, size=7.4):
        for i, t in enumerate(lines):
            self.text(t, self.w / 2, y + i * (size + 1.5), size, anchor='mt')

    def text(self, t, x, y, size, anchor='mm', bold=True, fill=(30, 33, 36, 255)):
        self.d.text((f(x), f(y)), t, font=font(size, bold), fill=fill, anchor=anchor)

    def vtext(self, t, cx, cy, size=6.4):
        fnt = font(size)
        bb = self.d.textbbox((0, 0), t, font=fnt)
        tw, th = bb[2] - bb[0] + f(2), bb[3] - bb[1] + f(2)
        tmp = Image.new('RGBA', (tw, th), (0, 0, 0, 0))
        ImageDraw.Draw(tmp).text((-bb[0] + f(1), -bb[1] + f(1)), t, font=fnt, fill=(30, 33, 36, 255))
        tmp = tmp.rotate(90, expand=True)
        self.im.alpha_composite(tmp, (int(f(cx) - tmp.width / 2), int(f(cy) - tmp.height / 2)))

    # 37/50-pin connector, vertical
    def dsub(self, cx, cy, h=78, w=12):
        d = self.d
        x0, x1, y0, y1 = cx - w / 2, cx + w / 2, cy - h / 2, cy + h / 2
        d.rounded_rectangle((f(x0 - 2), f(y0 - 3), f(x1 + 2), f(y1 + 3)), radius=f(2), fill=(170, 174, 178, 255),
                            outline=(96, 100, 104, 255), width=f(0.7))
        d.polygon([(f(x0), f(y0)), (f(x1), f(y0 + 4)), (f(x1), f(y1 - 4)), (f(x0), f(y1))], fill=(24, 26, 28, 255))
        d.line([(f(x0 + 2), f(y0 + 3)), (f(x0 + 2), f(y1 - 3))], fill=(70, 74, 78, 255), width=f(0.6))
        for sy in (y0 - 8, y1 + 8):
            d.ellipse((f(cx - 3.3), f(sy - 3.3), f(cx + 3.3), f(sy + 3.3)), fill=(188, 192, 196, 255), outline=(90, 94, 98, 255), width=f(0.6))
            d.ellipse((f(cx - 1.4), f(sy - 1.4), f(cx + 1.4), f(sy + 1.4)), fill=(70, 74, 78, 255))

    def led(self, cx, cy, col, r=2.0):
        d = self.d
        d.ellipse((f(cx - r - 0.6), f(cy - r - 0.6), f(cx + r + 0.6), f(cy + r + 0.6)), fill=(60, 64, 68, 255))
        d.ellipse((f(cx - r), f(cy - r), f(cx + r), f(cy + r)), fill=col)
        d.ellipse((f(cx - r * 0.5), f(cy - r * 0.6), f(cx), f(cy - r * 0.1)), fill=(255, 255, 255, 110))

    def bar_led(self, x, y, col, w=8, h=3.2):
        self.d.rectangle((f(x), f(y), f(x + w), f(y + h)), fill=col, outline=(40, 60, 40, 255), width=f(0.4))

    def led_grid(self, x, y, labels_left, labels_right, colors, title=None):
        if title:
            for i, t in enumerate(title):
                self.text(t, x + 9.5, y - 10 + i * 7.5 - (len(title) - 1) * 7.5, 5.6)
        rows = len(labels_left)
        for r in range(rows):
            yy = y + r * 5.2
            self.text(labels_left[r], x - 2.5, yy + 1.6, 4.6, bold=False)
            self.bar_led(x, yy, colors[r][0])
            self.bar_led(x + 10, yy, colors[r][1])
            self.text(labels_right[r], x + 21.5, yy + 1.6, 4.6, bold=False)

    def xlr(self, cx, cy, r=7.5):
        d = self.d
        d.ellipse((f(cx - r - 1.5), f(cy - r - 1.5), f(cx + r + 1.5), f(cy + r + 1.5)), fill=(130, 134, 138, 255))
        d.ellipse((f(cx - r), f(cy - r), f(cx + r), f(cy + r)), fill=(44, 46, 50, 255), outline=(170, 174, 178, 255), width=f(1))
        d.ellipse((f(cx - r + 2.2), f(cy - r + 2.2), f(cx + r - 2.2), f(cy + r - 2.2)), fill=(66, 70, 74, 255))
        for a, b in ((-2.6, -1.4), (2.6, -1.4), (0, 2.8)):
            d.ellipse((f(cx + a - 1.1), f(cy + b - 1.1), f(cx + a + 1.1), f(cy + b + 1.1)), fill=(210, 190, 120, 255))

    def bnc(self, cx, cy, r=8):
        d = self.d
        pts = []
        import math
        for i in range(6):
            a = math.pi / 6 + i * math.pi / 3
            pts.append((f(cx + r * math.cos(a)), f(cy + r * math.sin(a))))
        d.polygon(pts, fill=(176, 180, 184, 255), outline=(90, 94, 98, 255))
        d.ellipse((f(cx - r + 2.5), f(cy - r + 2.5), f(cx + r - 2.5), f(cy + r - 2.5)), fill=(206, 210, 214, 255), outline=(110, 114, 118, 255), width=f(0.6))
        d.ellipse((f(cx - 2.2), f(cy - 2.2), f(cx + 2.2), f(cy + 2.2)), fill=(60, 64, 68, 255))
        d.ellipse((f(cx - 0.8), f(cy - 0.8), f(cx + 0.8), f(cy + 0.8)), fill=(220, 200, 130, 255))

    def optical(self, cx, cy, lit):
        d = self.d
        d.rectangle((f(cx - 8), f(cy - 9), f(cx + 8), f(cy + 9)), fill=(28, 30, 32, 255), outline=(100, 104, 108, 255), width=f(0.7))
        d.rectangle((f(cx - 5), f(cy - 6), f(cx + 5), f(cy + 6)), fill=(52, 54, 58, 255))
        d.ellipse((f(cx - 2.6), f(cy - 2.6), f(cx + 2.6), f(cy + 2.6)), fill=(220, 40, 40, 255) if lit else (150, 152, 156, 255))

    def save(self, name):
        im = self.im.resize((int(self.w), H), Image.LANCZOS)
        im.save(os.path.join(OUT, name + '.png'))


GREEN, GREEN_HI, YELLOW, RED = (40, 170, 70, 255), (90, 230, 110, 255), (225, 200, 40, 255), (220, 40, 40, 255)


def mic():
    c = Card(45)
    c.title(['MIC', 'CARD'])
    c.led_grid(13, 88, ['1', '2', '3', '4'], ['', '', '', ''], [(YELLOW, GREEN)] * 4, title=['48V  SIG'])
    c.dsub(30, 190)
    c.vtext('4x MIC IN / SPLIT OUT', 12, 190)
    c.save('mic')


def dl5mic():
    c = Card(45)
    c.title(['DL5', 'MIC'])
    for i in range(8):
        y = 48 + i * 25
        c.xlr(26, y)
        c.led(9, y + 7, RED, r=1.8)
    c.save('dl5mic')


def aesebu():
    c = Card(91)
    c.title(['AES/EBU CARD'])
    c.led_grid(26, 92, ['1', '2', '3', '4'], ['5', '6', '7', '8'], [(GREEN, GREEN_HI)] * 4)
    c.text('LOCK', 66, 99, 6.2)
    c.dsub(34, 196)
    c.dsub(70, 196)
    c.vtext('AES I/O 1-8', 19, 196)
    c.vtext('AES I/O 9-16', 55, 196)
    c.save('aesebu')


def adat():
    c = Card(45)
    c.title(['ADAT', 'CARD'])
    for i, (lab, lit) in enumerate((('IN 1-8', False), ('IN 9-16', False), ('OUT 1-8', True), ('OUT 9-16', True))):
        y = 60 + i * 50
        if not lit:
            c.led(16, y - 15, GREEN, r=2.2)
        c.optical(16, y + 2, lit)
        c.vtext(lab, 35, y + 2, 5.8)
    c.save('adat')


def gpio():
    c = Card(91)
    c.title(['GPIO CARD'])
    c.dsub(34, 190)
    c.dsub(70, 190)
    c.vtext('GPO 1-16', 19, 190)
    c.vtext('GPI 1-16', 55, 190)
    c.save('gpio')


def linein():
    c = Card(45)
    c.title(['LINE', 'IN'])
    c.led_grid(10, 92, ['1', '2', '3', '4'], ['5', '6', '7', '8'], [(GREEN, GREEN_HI)] * 4, title=['SIGNAL'])
    c.dsub(30, 196)
    c.vtext('8x LINE IN', 12, 196)
    c.save('linein')


def lineout():
    c = Card(45)
    c.title(['LINE', 'OUT'])
    c.dsub(30, 196)
    c.vtext('8x LINE OUT', 12, 196)
    c.save('lineout')


def tdif():
    c = Card(91)
    c.title(['TDIF CARD'])
    c.bnc(26, 64)
    c.text('SYNC 1', 58, 64, 6.2)
    c.bnc(26, 98)
    c.text('SYNC 2', 58, 98, 6.2)
    c.dsub(34, 200)
    c.dsub(70, 200)
    c.vtext('TDIF I/O 1-8', 19, 200)
    c.vtext('TDIF I/O 9-16', 55, 200)
    c.save('tdif')


def unknown():
    c = Card(45)
    c.title(['OTHER', 'CARD'])
    c.text('?', 22.5, 140, 22, fill=(150, 154, 158, 255))
    c.save('unknown')


def power():
    c = Card(136)
    d = c.d
    c.title(['POWER SUPPLY'], y=16, size=7.6)
    # mains inlet with switch
    x0, y0 = 30, 46
    d.rounded_rectangle((f(x0), f(y0), f(x0 + 64), f(y0 + 132)), radius=f(6), fill=(52, 56, 60, 255), outline=(30, 32, 34, 255), width=f(1))
    d.rounded_rectangle((f(x0 + 9), f(y0 + 12), f(x0 + 55), f(y0 + 58)), radius=f(2), fill=(26, 28, 30, 255), outline=(90, 94, 98, 255), width=f(1))
    d.rounded_rectangle((f(x0 + 13), f(y0 + 16), f(x0 + 51), f(y0 + 54)), radius=f(2), fill=(44, 46, 50, 255))
    c.text('I', x0 + 32, y0 + 26, 8, fill=(220, 222, 224, 255))
    c.text('O', x0 + 32, y0 + 44, 7, fill=(220, 222, 224, 255))
    d.polygon([(f(x0 + 9), f(y0 + 70)), (f(x0 + 55), f(y0 + 70)), (f(x0 + 55), f(y0 + 104)), (f(x0 + 47), f(y0 + 116)),
               (f(x0 + 17), f(y0 + 116)), (f(x0 + 9), f(y0 + 104))], fill=(20, 22, 24, 255), outline=(90, 94, 98, 255))
    for px, py in ((x0 + 21, y0 + 86), (x0 + 43, y0 + 86), (x0 + 32, y0 + 102)):
        d.rectangle((f(px - 1.4), f(py - 5), f(px + 1.4), f(py + 5)), fill=(200, 202, 204, 255))
    for sy in (y0 + 6, y0 + 124):
        d.ellipse((f(x0 + 28.5), f(sy - 3.5), f(x0 + 35.5), f(sy + 3.5)), fill=(150, 154, 158, 255))
    c.text('100-240 V~', 62, 192, 6.8)
    c.text('50/60 Hz', 62, 201, 6.0, bold=False)
    # earth terminal
    gx, gy = 26, 228
    d.regular_polygon((f(gx), f(gy), f(8)), 6, fill=(188, 170, 110, 255), outline=(110, 96, 50, 255))
    d.ellipse((f(gx - 3), f(gy - 3), f(gx + 3), f(gy + 3)), fill=(150, 132, 70, 255))
    ex = gx + 22  # earth symbol
    d.line((f(ex), f(gy - 8), f(ex), f(gy - 1)), fill=(40, 42, 44, 255), width=f(1))
    for i, ww in enumerate((10, 6.5, 3)):
        d.line((f(ex - ww / 2), f(gy - 1 + i * 3), f(ex + ww / 2), f(gy - 1 + i * 3)), fill=(40, 42, 44, 255), width=f(1))
    # status connector
    c.dsub(117, 228, h=40, w=10)
    c.vtext('STATUS', 104, 228, 6)
    c.save('power')


def fader(name, top, bottom, line):
    w, h = 24, 44
    im = Image.new('RGBA', (f(w), f(h)), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    for y in range(f(h)):
        t = y / f(h)
        c = [int(top[i] + (bottom[i] - top[i]) * t) for i in range(3)]
        d.line((0, y, f(w), y), fill=tuple(c) + (255,))
    mask = Image.new('L', im.size, 0)
    ImageDraw.Draw(mask).rounded_rectangle((0, 0, f(w) - 1, f(h) - 1), radius=f(3), fill=255)
    im.putalpha(mask)
    for i in range(7):
        y = 5 + i * 2.2
        d.line((f(3), f(y), f(w - 3), f(y)), fill=(0, 0, 0, 70), width=f(0.6))
        d.line((f(3), f(h - y), f(w - 3), f(h - y)), fill=(0, 0, 0, 70), width=f(0.6))
    d.rectangle((f(2), f(h / 2 - 1), f(w - 2), f(h / 2 + 1)), fill=line)
    d.rounded_rectangle((0, 0, f(w) - 1, f(h) - 1), radius=f(3), outline=(20, 22, 24, 255), width=f(0.8))
    im.resize((w, h), Image.LANCZOS).save(os.path.join(OUT, name + '.png'))


if __name__ == '__main__':
    for fn in (mic, dl5mic, aesebu, adat, gpio, linein, lineout, tdif, unknown, power):
        fn()
    fader('fader_normal', (122, 126, 130), (62, 66, 70), (235, 237, 240, 255))
    fader('fader_touch', (150, 156, 162), (80, 86, 92), (255, 255, 255, 255))
    fader('fader_down', (92, 96, 100), (44, 48, 52), (200, 204, 208, 255))
    print('done')
