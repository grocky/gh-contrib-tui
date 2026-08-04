#!/usr/bin/env python3
"""Convert a captured ANSI terminal frame into a self-contained SVG.

Only what lipgloss emits is handled: truecolor foreground and background, bold,
faint, and reset. Anything else is ignored rather than guessed at.
"""
import re
import sys
from xml.sax.saxutils import escape

CHAR_W = 8.4
LINE_H = 17.0
FONT_SIZE = 14
PAD_X, PAD_Y = 16, 14

BG = "#0d1117"
FG = "#c9d1d9"

SGR = re.compile(r"\x1b\[([0-9;]*)m")

# The charts are drawn out of block characters, which are geometry rather than
# writing: their advance varies by font, and a glyph wider than its cell paints
# over whatever follows it. Each is drawn as a rectangle instead -- the fraction
# of the cell it fills, and how solid it is.
BLOCKS = {
    "\u2588": (0.0, 1.0, 1.00),  # full
    "\u2580": (0.0, 0.5, 1.00),  # upper half
    "\u2584": (0.5, 0.5, 1.00),  # lower half
    "\u2593": (0.0, 1.0, 0.75),  # dark shade
    "\u2592": (0.0, 1.0, 0.50),  # medium shade
    "\u2591": (0.0, 1.0, 0.25),  # light shade
    "\u25a0": (0.15, 0.7, 1.00),  # small square, the dense calendar cell
}


class State:
    def __init__(self):
        self.fg = None
        self.bg = None
        self.bold = False
        self.faint = False

    def copy(self):
        s = State()
        s.fg, s.bg, s.bold, s.faint = self.fg, self.bg, self.bold, self.faint
        return s

    def apply(self, params):
        codes = [int(p) for p in params.split(";") if p != ""] or [0]
        i = 0
        while i < len(codes):
            c = codes[i]
            if c == 0:
                self.__init__()
            elif c == 1:
                self.bold = True
            elif c == 2:
                self.faint = True
            elif c == 22:
                self.bold = self.faint = False
            elif c == 39:
                self.fg = None
            elif c == 49:
                self.bg = None
            elif c in (38, 48) and i + 4 < len(codes) and codes[i + 1] == 2:
                rgb = "#%02x%02x%02x" % (codes[i + 2], codes[i + 3], codes[i + 4])
                if c == 38:
                    self.fg = rgb
                else:
                    self.bg = rgb
                i += 4
            elif c in (38, 48) and i + 2 < len(codes) and codes[i + 1] == 5:
                i += 2  # 256-colour: not emitted here, skip its argument
            i += 1


def runs(line):
    """Split one line into (text, state) runs."""
    state, out, pos = State(), [], 0
    for match in SGR.finditer(line):
        if match.start() > pos:
            out.append((line[pos:match.start()], state.copy()))
        state.apply(match.group(1))
        pos = match.end()
    if pos < len(line):
        out.append((line[pos:], state.copy()))
    return out


def convert(text, title):
    lines = text.rstrip("\n").split("\n")
    cols = max((len(SGR.sub("", ln)) for ln in lines), default=0)
    width = int(cols * CHAR_W) + PAD_X * 2
    height = int(len(lines) * LINE_H) + PAD_Y * 2

    parts = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" '
        f'viewBox="0 0 {width} {height}" font-family="ui-monospace, SFMono-Regular, '
        f'Menlo, Consolas, &quot;DejaVu Sans Mono&quot;, monospace" font-size="{FONT_SIZE}">',
        f"<title>{escape(title)}</title>",
        f'<rect width="{width}" height="{height}" rx="8" fill="{BG}"/>',
    ]

    # Backgrounds first, so no glyph is painted over by a later cell's fill.
    for row, line in enumerate(lines):
        col = 0
        y = PAD_Y + row * LINE_H
        for text_run, st in runs(line):
            if st.bg and text_run.strip(" ") != "":
                x = PAD_X + col * CHAR_W
                w = len(text_run) * CHAR_W
                parts.append(
                    f'<rect x="{x:.1f}" y="{y:.1f}" width="{w:.1f}" '
                    f'height="{LINE_H:.1f}" fill="{st.bg}"/>'
                )
            col += len(text_run)

    for row, line in enumerate(lines):
        col = 0
        y = PAD_Y + row * LINE_H
        baseline = y + FONT_SIZE * 0.8
        spans = []
        for text_run, st in runs(line):
            # Leading spaces are dropped and paid for in x instead: renderers
            # disagree about xml:space, and a collapsed run shifts a whole line.
            stripped = text_run.strip(" ")
            if stripped != "" and all(ch in BLOCKS for ch in stripped):
                # A run of pure geometry: draw it, do not typeset it.
                lead = len(text_run) - len(text_run.lstrip(" "))
                for offset, ch in enumerate(stripped):
                    top, frac, alpha = BLOCKS[ch]
                    parts.append(
                        f'<rect x="{PAD_X + (col + lead + offset) * CHAR_W:.2f}" '
                        f'y="{y + top * LINE_H:.2f}" width="{CHAR_W:.2f}" '
                        f'height="{frac * LINE_H:.2f}" fill="{st.fg or FG}"'
                        + (f' opacity="{alpha}"' if alpha < 1 else "")
                        + "/>"
                    )
                col += len(text_run)
                continue
            if stripped != "":
                lead = len(text_run) - len(text_run.lstrip(" "))
                x = PAD_X + (col + lead) * CHAR_W
                # The run is pinned to the columns it occupies. Block glyphs
                # are wider than the cell in some fallback fonts, and without
                # this they spill over whatever follows them.
                attrs = [
                    f'x="{x:.1f}"',
                    f'fill="{st.fg or FG}"',
                    f'textLength="{len(stripped) * CHAR_W:.1f}"',
                    'lengthAdjust="spacingAndGlyphs"',
                ]
                if st.bold:
                    attrs.append('font-weight="600"')
                if st.faint and not st.fg:
                    attrs.append('opacity="0.65"')
                spans.append(
                    f'<tspan {" ".join(attrs)}>{escape(stripped)}</tspan>'
                )
            col += len(text_run)
        if spans:
            parts.append(
                f'<text y="{baseline:.1f}" xml:space="preserve">{"".join(spans)}</text>'
            )

    parts.append("</svg>")
    return "\n".join(parts) + "\n"


if __name__ == "__main__":
    src, dst, title = sys.argv[1], sys.argv[2], sys.argv[3]
    with open(src, encoding="utf-8") as fh:
        svg = convert(fh.read(), title)
    with open(dst, "w", encoding="utf-8") as fh:
        fh.write(svg)
    print(f"{dst}: {len(svg)} bytes")
