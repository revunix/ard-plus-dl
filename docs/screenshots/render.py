#!/usr/bin/env python3
"""Turn the ANSI dumps of the TUI into the images of the README.

The Go test internal/tui/shots_test.go writes the screens as ANSI files, this
script renders them as SVG and converts them to PNG with the thumbnail tool of
macOS. Run it from the root of the repository:

    python3 docs/screenshots/render.py
"""
import html
import os
import re
import subprocess
import sys

CELL_WIDTH = 12.0
CELL_HEIGHT = 28.0
FONT_SIZE = 20.0
PADDING = 24.0

SGR = re.compile(r"\x1b\[([0-9;]*)m")


def segments(line):
    """Splits a rendered line into (text, foreground, background, bold) parts."""
    parts = []
    fg = None
    bg = None
    bold = False
    pos = 0
    for match in SGR.finditer(line):
        chunk = line[pos:match.start()]
        if chunk:
            parts.append((chunk, fg, bg, bold))
        codes = [c for c in match.group(1).split(";") if c != ""] or ["0"]
        index = 0
        while index < len(codes):
            code = codes[index]
            if code == "0":
                fg = bg = None
                bold = False
            elif code == "1":
                bold = True
            elif code == "22":
                bold = False
            elif code == "38" and index + 4 < len(codes) and codes[index + 1] == "2":
                fg = rgb(codes[index + 2:index + 5])
                index += 4
            elif code == "48" and index + 4 < len(codes) and codes[index + 1] == "2":
                bg = rgb(codes[index + 2:index + 5])
                index += 4
            elif code == "39":
                fg = None
            elif code == "49":
                bg = None
            index += 1
        pos = match.end()
    if line[pos:]:
        parts.append((line[pos:], fg, bg, bold))
    return parts


def rgb(values):
    return "#%02X%02X%02X" % tuple(int(v) for v in values)


def surface(line):
    """Returns the background color of the line."""
    for _, _, bg, _ in segments(line):
        if bg:
            return bg
    return "#0F141B"


def render(name, lines, target):
    while lines and not lines[-1].strip():
        lines.pop()
    width = max((len(strip_ansi(line)) for line in lines), default=80)
    svg_width = width * CELL_WIDTH + 2 * PADDING
    svg_height = len(lines) * CELL_HEIGHT + 2 * PADDING
    out = [
        '<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" '
        'viewBox="0 0 %.0f %.0f">' % (svg_width, svg_height, svg_width, svg_height),
        '<rect width="%.0f" height="%.0f" fill="#0F141B"/>' % (svg_width, svg_height),
        '<rect x="%.0f" y="%.0f" width="%.0f" height="%.0f" fill="#0F141B" rx="8"/>'
        % (PADDING / 2, PADDING / 2, svg_width - PADDING, svg_height - PADDING),
    ]
    for row, line in enumerate(lines):
        top = PADDING + row * CELL_HEIGHT
        base = surface(line)
        out.append(
            '<rect x="%.0f" y="%.0f" width="%.0f" height="%.0f" fill="%s"/>'
            % (PADDING / 2, top, svg_width - PADDING, CELL_HEIGHT, base))
        column = 0
        for text, fg, bg, bold in segments(line):
            width_cells = len(text)
            if bg:
                out.append(
                    '<rect x="%.0f" y="%.0f" width="%.0f" height="%.0f" fill="%s"/>'
                    % (PADDING / 2 + column * CELL_WIDTH, top,
                       width_cells * CELL_WIDTH, CELL_HEIGHT, bg))
            if text.strip():
                style = "font-family=\"Menlo, Monaco, monospace\" font-size=\"%g\"" % FONT_SIZE
                if fg:
                    style += ' fill="%s"' % fg
                else:
                    style += ' fill="#F4F8FF"'
                if bold:
                    style += ' font-weight="700"'
                # Non breaking spaces keep the alignment of the columns.
                body = html.escape(text).replace(" ", "&#160;")
                out.append(
                    '<text x="%.0f" y="%.0f" %s xml:space="preserve">%s</text>'
                    % (PADDING / 2 + column * CELL_WIDTH, top + CELL_HEIGHT - 6,
                       style, body))
            column += width_cells
    out.append("</svg>")
    svg = "\n".join(out)
    svg_path = os.path.join(target, name + ".svg")
    with open(svg_path, "w", encoding="utf-8") as handle:
        handle.write(svg)
    png_path = os.path.join(target, name + ".png")
    # sips renders the vector image in its own size, so the text stays sharp.
    subprocess.run(["sips", "-s", "format", "png", svg_path, "--out", png_path],
                   check=True, stdout=subprocess.DEVNULL)
    os.remove(svg_path)
    return png_path


def strip_ansi(line):
    return SGR.sub("", line)


def main():
    root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    target = os.path.join(root, "docs", "screenshots")
    shots = os.environ.get("DUMP_SHOTS", "/tmp/shots")
    os.makedirs(target, exist_ok=True)

    env = dict(os.environ, DUMP_SHOTS=shots)
    subprocess.run(["go", "test", "./internal/tui/", "-run", "TestShots"],
                   cwd=root, env=env, check=True, stdout=subprocess.DEVNULL)

    for name in sorted(os.listdir(shots)):
        if not name.endswith(".ansi"):
            continue
        with open(os.path.join(shots, name), encoding="utf-8") as handle:
            lines = handle.read().split("\n")
        path = render(name[:-len(".ansi")], lines, target)
        print("wrote", os.path.relpath(path, root))


if __name__ == "__main__":
    main()
