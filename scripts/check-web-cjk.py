#!/usr/bin/env python3
"""Check whether Chinese (Han characters) has leaked into the HTML of the statically exported web UI (`web/out`).

The key achievement and differentiator of this fork is that "there is no Chinese
on the screens users see" (the Han-zero property of the `out` HTML established by
backlog item B4). If that property were left to manual verification, a regression
such as a contributor hardcoding a Chinese string or a missing translation would
stay silently broken until the next person noticed it by eye. This script blocks
such regressions at the merge gate.

How it checks
-------------
It scans the **raw contents** of every `.html` file under `web/out` (not just the
rendered markup but also inline scripts and React Server Components flight data)
and exits with code 1 if there is even one Han character. The raw contents are
used because the text users see rides along in the HTML as flight data
(JSON-shaped strings). Stripping only the tags would miss leaks through that path.

Out of scope (honest limitation)
--------------------------------
This gate only looks at the static export output. It therefore cannot check
**what the browser draws at runtime in MOCK mode (`NEXT_PUBLIC_MOCK=1`, the mode
the public demo uses)**. `web/src/lib/mock/data.ts`, `handler.ts`, and each
page's hardcoded samples are fetched via `mockHandle()` by a `useEffect` after
hydration and rendered as React state, so they ship only in the runtime JS
bundle, not in the `out` HTML. Chinese that enters through this path therefore
passes this gate (the sample logs of the public demo's `/system/logs` once kept
Chinese silently that way, and backlog G114 fixed it to Korean).

That surface is guarded by source review. Any Chinese remaining in render-target
files (`.tsx` and client data under `web/src`) must be one of the following: a
code comment, an i18n fallback that is blocked by `t.has(...)` and never actually
used, a backend wire-format token (the chat-mention classification labels and the
Chinese "model" decision-source sentinel), a branch literal shown only when the
locale is `zh`, or a mirror that reflects verbatim the agent-brain (segment [A])
prompt, which is left untranslated to preserve performance. If it belongs to none
of these and is "just Chinese visible on screen", translate it to Korean (backlog
G115 audited this classification exhaustively).

What is allowed and detected
----------------------------
- **Hangul (`U+AC00-U+D7A3`, etc.) is allowed.** The Han block and the Hangul
  block do not overlap in Unicode, so detecting only the Han block lets Hangul
  pass automatically.
- **Only Han (CJK ideographs) is detected**: Unified Ideographs plus Extension A
  (U+3400-U+9FFF), Compatibility Ideographs (U+F900-U+FAFF), and Extension B and
  above in the supplementary planes (U+20000 and up). Kana and fullwidth
  punctuation are not "Han", so they are outside this gate's scope (the range is
  narrowed to Han to prevent false positives from verbatim-preserved code blocks
  or evidence quotations).

Intentional Han that must be preserved from upstream
----------------------------------------------------
The `out` HTML currently contains not a single Han character, so the allowlist
is empty. If a case arises in future where the original text (Chinese) must be
deliberately left on screen to preserve performance (e.g. the language
switcher's Chinese-language label), add that character to `ALLOWED_HAN` below
and note in a comment why it is kept. That keeps the gate strict while letting
only the intended exceptions through narrowly.

Uses only the standard library and makes no network connections.
Run: `python3 -I scripts/check-web-cjk.py [out directory]`
(The default is `web/out` at the repository root. CI calls it from the `web`
working directory as `python3 -I ../scripts/check-web-cjk.py`.)
"""
import os
import re
import subprocess
import sys

# CJK Han blocks only. Hangul (AC00-D7A3), kana, and fullwidth punctuation are not included.
# Ranges: Extension A + Unified Ideographs (U+3400-U+9FFF),
#         Compatibility Ideographs (U+F900-U+FAFF),
#         supplementary-plane Extension B and above (U+20000-U+2FFFF).
HAN = re.compile(r"[㐀-鿿豈-﫿\U00020000-\U0002ffff]")

# Han characters deliberately left on screen (e.g. the language switcher's Chinese label). Currently empty --
# while it is empty, any Han character trips the gate. When adding an exception, write the character and the reason together.
# e.g. ALLOWED_HAN = {"\u4e2d", "\u6587"}  # language switcher Chinese label (for upstream comparison)
ALLOWED_HAN: set = set()


def find_out_dir() -> str:
    """Decide which `out` directory to check.

    Use the path given as an argument if there is one; otherwise use `web/out` at the git repository root.
    """
    if len(sys.argv) > 1:
        return os.path.abspath(sys.argv[1])
    root = subprocess.check_output(
        ["git", "rev-parse", "--show-toplevel"], text=True
    ).strip()
    return os.path.join(root, "web", "out")


def main() -> int:
    out_dir = find_out_dir()
    if not os.path.isdir(out_dir):
        print(
            f"Error: the directory to check does not exist -- {out_dir}\n"
            "First create the static export with `npm run build:static`, then run this again."
        )
        return 2

    checked = 0
    offenders = []  # (relative path, Han count, preview)
    for dirpath, _dirnames, filenames in os.walk(out_dir):
        for name in filenames:
            if not name.endswith(".html"):
                continue
            checked += 1
            path = os.path.join(dirpath, name)
            with open(path, encoding="utf-8", errors="ignore") as fh:
                text = fh.read()
            found = [ch for ch in HAN.findall(text) if ch not in ALLOWED_HAN]
            if found:
                rel = os.path.relpath(path, out_dir)
                kinds = "".join(sorted(set(found)))
                offenders.append((rel, len(found), kinds))

    print(f"Checked {checked} HTML files (target: {out_dir})")
    if offenders:
        print(f"Chinese (Han) leak in {len(offenders)} files -- user-facing HTML contains Han characters:")
        for rel, count, kinds in offenders:
            preview = kinds if len(kinds) <= 30 else kinds[:30] + "…"
            print(f"  out/{rel}: {count} Han characters · kinds {preview}")
        print(
            "\nTranslate them to Korean, or, if they are intentional Han characters that must be "
            "preserved from upstream, add them to ALLOWED_HAN in scripts/check-web-cjk.py along with the reason."
        )
        return 1
    if checked == 0:
        print("Warning: no HTML was checked -- verify that the build completed correctly.")
        return 2
    print("0 Chinese (Han) leaks -- user-facing HTML contains no Han characters.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
