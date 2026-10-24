#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Turns the user interface in web/ into web/assets_gen.go for the old-macOS
build (Go 1.12 has no embed package), adapted to the WebKit of macOS
10.10-10.15:

  app.js     web/legacy/polyfills.js + app.js lowered to ES2016 by esbuild
  style.css  gap -> grid-gap and margin fallbacks (html.nogap), inset ->
             top/right/bottom/left, -webkit- prefixes, + web/legacy/legacy.css
  others     as they are

esbuild: $ESBUILD, else "esbuild" on the PATH, else "npx --yes esbuild@0.21.5".
"""
import base64
import os
import re
import shutil
import subprocess
import sys

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
WEB = os.path.join(ROOT, "web")


def esbuild_cmd():
    if os.environ.get("ESBUILD"):
        return [os.environ["ESBUILD"]]
    if shutil.which("esbuild"):
        return ["esbuild"]
    return ["npx", "--yes", "esbuild@0.21.5"]


def lower_js(src):
    out = subprocess.run(esbuild_cmd() + ["--target=es2016", "--loader=js", "--log-level=warning"],
                         input=src.encode(), stdout=subprocess.PIPE, check=True).stdout.decode()
    if "?." in re.sub(r'"(?:[^"\\]|\\.)*"|\'(?:[^\'\\]|\\.)*\'|`(?:[^`\\]|\\.)*`', "", out):
        sys.exit("legacyweb: optional chaining left in app.js")
    return out


# ---------- CSS ----------

def split_rules(css):
    """Yields (prelude, body, is_block_at_rule) for the top level of css."""
    i, n = 0, len(css)
    while i < n:
        j = css.find("{", i)
        if j < 0:
            yield css[i:], None, False
            return
        prelude = css[i:j]
        depth, k = 1, j + 1
        while k < n and depth:
            if css[k] == "{":
                depth += 1
            elif css[k] == "}":
                depth -= 1
            k += 1
        body = css[j + 1:k - 1]
        yield prelude, body, prelude.strip().startswith("@") and "{" in body
        i = k


def decls(body):
    out = []
    for part in re.split(r";(?![^(]*\))", body):
        if ":" in part:
            p, v = part.split(":", 1)
            out.append((p.strip(), v.strip()))
    return out


def strip_comments(css):
    return re.sub(r"/\*.*?\*/", "", css, flags=re.S)


def display_of(css):
    """display and flex settings per selector, over the whole sheet."""
    info = {}

    def walk(c):
        for prelude, body, nested in split_rules(c):
            if body is None:
                continue
            if nested:
                walk(body)
                continue
            for sel in [s.strip() for s in prelude.split(",")]:
                d = info.setdefault(sel, {})
                for p, v in decls(body):
                    if p in ("display", "flex-direction", "flex-wrap", "grid-template-columns", "grid-template-rows", "grid-template"):
                        d[p] = v
    walk(css)
    return info


def fix_rule(prelude, body, info, extra):
    sels = [s.strip() for s in prelude.split(",")]
    ds = decls(body)
    props = dict(ds)
    add = []
    for p, v in ds:
        if p == "inset":
            vals = v.split()
            t, r, b, l = (vals * 4)[:4] if len(vals) == 1 else \
                (vals[0], vals[1], vals[0], vals[1]) if len(vals) == 2 else \
                (vals[0], vals[1], vals[2], vals[1]) if len(vals) == 3 else vals[:4]
            add += [("top", t), ("right", r), ("bottom", b), ("left", l)]
        if p == "position" and v.startswith("sticky"):
            add.insert(0, ("position", "-webkit-sticky"))
        if p == "user-select" and "-webkit-user-select" not in props:
            add.append(("-webkit-user-select", v))
        if p in ("gap", "row-gap", "column-gap"):
            vals = v.split()
            row, col = (vals[0], vals[-1]) if p == "gap" else (v, None) if p == "row-gap" else (None, v)
            # what kind of box is it?
            disp, d = props.get("display", ""), {}
            for s in sels:
                d = info.get(s, {})
                disp = disp or d.get("display", "")
            grid = "grid" in disp or any(k.startswith("grid-template") for k in list(props) + list(d))
            if grid:
                add.append(("grid-" + p, v))
            elif "flex" in disp:
                direction = props.get("flex-direction", d.get("flex-direction", "row"))
                wrap = props.get("flex-wrap", d.get("flex-wrap", "nowrap"))
                column = direction.startswith("column")
                if wrap.startswith("wrap"):
                    m = []
                    if col and col != "0":
                        m.append("margin-right: " + col)
                    if row and row != "0":
                        m.append("margin-bottom: " + row)
                    if m:
                        extra.append(", ".join(".nogap " + s + " > *" for s in sels) + " { " + "; ".join(m) + "; }")
                else:
                    g = row if column else col
                    if g and g != "0":
                        side = "top" if column else ("right" if direction == "row-reverse" else "left")
                        extra.append(", ".join(".nogap " + s + " > * + *" for s in sels) + " { margin-" + side + ": " + g + "; }")
    if not add:
        return body
    return body.rstrip().rstrip(";") + "; " + "; ".join(p + ": " + v for p, v in add) + "; "


def lower_css(css):
    css = strip_comments(css)
    info = display_of(css)
    extra = []

    def walk(c):
        out = []
        for prelude, body, nested in split_rules(c):
            if body is None:
                out.append(prelude)
            elif nested or prelude.strip().startswith("@keyframes"):
                out.append(prelude + "{" + (walk(body) if nested and not prelude.strip().startswith("@keyframes") else body) + "}")
            else:
                out.append(prelude + "{" + fix_rule(prelude, body, info, extra) + "}")
        return "".join(out)
    return walk(css) + "\n/* flex gap fallbacks */\n" + "\n".join(extra) + "\n"


def main():
    files = {}
    for dirpath, _, names in os.walk(WEB):
        rel = os.path.relpath(dirpath, WEB)
        if rel.split(os.sep)[0] == "legacy":
            continue
        for n in names:
            if n.endswith(".go"):
                continue
            path = os.path.join(dirpath, n)
            key = n if rel == "." else rel.replace(os.sep, "/") + "/" + n
            files[key] = open(path, "rb").read()
    poly = open(os.path.join(WEB, "legacy", "polyfills.js")).read()
    files["app.js"] = (poly + "\n" + lower_js(files["app.js"].decode())).encode()
    files["style.css"] = (lower_css(files["style.css"].decode()) + open(os.path.join(WEB, "legacy", "legacy.css")).read()).encode()

    out = ["// Code generated by macos/legacyweb.py. DO NOT EDIT.", "", "package web", "", 'import "encoding/base64"', "",
           "func init() {", "\tfor name, data := range map[string]string{"]
    for k in sorted(files):
        out.append("\t\t%s: %s," % (quote(k), quote(base64.b64encode(files[k]).decode())))
    out += ["\t} {", "\t\tb, err := base64.StdEncoding.DecodeString(data)", "\t\tif err != nil {", "\t\t\tpanic(err)", "\t\t}",
            "\t\tFiles[name] = b", "\t}", "}", ""]
    open(os.path.join(WEB, "assets_gen.go"), "w").write("\n".join(out))
    if len(sys.argv) > 1:  # also write the processed files to a folder, for checking
        for k, v in files.items():
            p = os.path.join(sys.argv[1], k)
            os.makedirs(os.path.dirname(p), exist_ok=True)
            open(p, "wb").write(v)
    print("legacyweb: %d files" % len(files))


def quote(s):
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


if __name__ == "__main__":
    main()
