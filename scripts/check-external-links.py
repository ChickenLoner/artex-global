#!/usr/bin/env python3
"""Check that the external links (http/https) referenced by tracked Markdown documents are still alive.

The sister script `check-doc-links.py` only looks at internal links and anchors
inside the repository and, by design, skips external URLs (they depend on the
network and are flaky, so the merge gate does not cover them). This script
handles that "separate check". The external links that the top of the README, the
defense guides, and the detections README tell visitors and defenders to "go
look at" (the incident-reporting desks boho.or.kr, privacy.go.kr, pipc.go.kr,
fsec.or.kr; CISA KEV; OWASP, SigmaHQ, Suricata, MITRE, MISP; the original demo
artex-demo.vercel.app; GitHub badges; and so on) would silently stay broken if
they were altered, moved, or shut down; this script catches that periodically or
on demand.

Rules for choosing which URLs to check
- Scan every tracked `.md`, but skip URLs inside fenced code blocks (``` or ~~~)
  and inline code spans (`` `...` ``). URLs there are command examples, config
  values, or quoted external assets (e.g. `id.redhaze.top` in the side-question
  verification doc), not "reference links for the reader to follow".
- Collect http/https addresses from Markdown links and images (`[text](url)`,
  `![alt](url)`), autolinks (`<url>`), and bare URLs in the remaining prose.
- Exclude reserved and placeholder hosts: localhost; private/loopback IPs
  (127., 10., 192.168., 169.254., 172.16-31., 0.0.0.0, ::1); RFC 2606/6761
  reserved names (example.com/org/net/edu, `*.example.*`, `.test`, `.invalid`,
  `.local`, `.tld`); and names that are not an FQDN because they have no dot
  (e.g. `target`).

How liveness is checked (MAINTAINING.md section 8.2's lesson turned into code)
Korean public-sector and security-agency sites reject HEAD requests and default
User-Agents, or redirect several times, so a naive check mistakes healthy links
for broken ones. This script therefore checks **with a browser User-Agent, using
GET, following redirects**. Results fall into three states.
- OK: the final status is 2xx or 3xx. The link is valid.
- RESTRICTED: 401, 403, 405, 429. The host is alive, but the way we check was
  blocked by the server's access policy (bot blocking, method rejection, rate
  limiting); the link is not broken. Reported, but not counted as a failure.
- DOWN: 404, 410, 5xx (even after retries), DNS/connection/timeout/SSL errors
  (even after retries). Likely actually broken.

Network errors, 5xx, and 429 are retried after a short delay to tell transient
blips from real outages. Uses only the standard library. This script only sends
GET requests to reference URLs that the repository's **public documents already
point to** in order to confirm liveness; it does not scan or probe any target.

Known exceptions (allowlist)
If a URL listed in `scripts/external-links-allowlist.txt` comes back DOWN, it is
classified separately as "ALLOWED" and is not counted toward the strict exit
code. This transparently records dead links that we do not own and cannot fix,
inherited by upstream verbatim-preserved files (e.g. the upstream
CHANGELOG.zh.md), so that the periodic strict check does not stay red forever
because of that one link and turns red **only when a newly broken link appears**.

Usage
- Default (report only, exit code 0): `python3 -I scripts/check-external-links.py`
- Periodic/release check rather than a merge gate (turns red on newly broken
  links): `--strict` (exit code 1 if there is even one DOWN not in the
  allowlist). RESTRICTED and ALLOWED are not counted as failures even in strict mode.
- Preview the extracted set without a network: `--list` (prints the URLs to
  check and their sources, without making any calls).
"""
import argparse
import http.client
import os
import re
import socket
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.request
from collections import defaultdict
from urllib.parse import urlsplit

# `[text](url)` body links (not images) and `![alt](url)` images. The path runs up to whitespace or a closing parenthesis.
MD_LINK = re.compile(r"(?<!\!)\[[^\]]*\]\(([^)\s]+)")
MD_IMAGE = re.compile(r"!\[[^\]]*\]\(([^)\s]+)")
# `<https://...>` autolinks.
AUTOLINK = re.compile(r"<(https?://[^>\s]+)>")
# Bare URLs in prose. Trailing punctuation is stripped afterwards.
BARE_URL = re.compile(r"https?://[^\s)>\]\"'`]+")
# Inline code spans `...` (single backtick). Blanked out before extraction.
INLINE_CODE = re.compile(r"`[^`]*`")
# Punctuation commonly attached to the end of a prose URL.
TRAILING_PUNCT = ".,;:!?\"'»)]}>"

# IPv4 private / loopback / link-local ranges (172.16-31. has its own pattern).
PRIVATE_IPV4 = re.compile(r"^(127\.|10\.|192\.168\.|169\.254\.|0\.0\.0\.0$)")
PRIVATE_IPV4_172 = re.compile(r"^172\.(1[6-9]|2\d|3[01])\.")

BROWSER_UA = (
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "
    "AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"
)

# Status codes meaning the host is alive but our way of checking was blocked by server policy (not broken).
RESTRICTED_CODES = {401, 403, 405, 429}

ALLOWLIST_PATH = os.path.join("scripts", "external-links-allowlist.txt")


def load_allowlist(root: str) -> set:
    """Read the set of known DOWN URLs that we cannot fix (an empty set if the file is absent).

    One URL per line. Anything after `#` is a comment; blank lines and comment-only lines are ignored.
    """
    path = os.path.join(root, ALLOWLIST_PATH)
    allow = set()
    if not os.path.exists(path):
        return allow
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            line = line.split("#", 1)[0].strip()
            if line:
                allow.add(line)
    return allow


def strip_code(line: str) -> str:
    """Remove inline code spans from a line outside any fence (URLs inside them are not references)."""
    return INLINE_CODE.sub(" ", line)


def is_checkable(url: str) -> bool:
    """Filter out reserved and placeholder hosts, keeping only external URLs worth actually checking."""
    parts = urlsplit(url)
    if parts.scheme not in ("http", "https"):
        return False
    host = parts.hostname
    if not host:
        return False
    host = host.lower()
    if host == "localhost" or host.endswith(".localhost"):
        return False
    if host == "::1":
        return False
    if PRIVATE_IPV4.match(host) or PRIVATE_IPV4_172.match(host):
        return False
    if host in ("example.com", "example.org", "example.net", "example.edu"):
        return False
    if host.endswith((".example.com", ".example.org", ".example.net", ".example")):
        return False
    if host.endswith((".test", ".invalid", ".local", ".localdomain", ".tld")):
        return False
    if "." not in host:  # A bare name that is not an FQDN (e.g. target)
        return False
    return True


def extract(root: str, md_files: list) -> dict:
    """Build a dict of external URL -> [(file, line number), ...] to check from the tracked .md files."""
    found = defaultdict(list)
    for md in md_files:
        with open(os.path.join(root, md), encoding="utf-8", errors="ignore") as fh:
            lines = fh.readlines()
        in_fence = False
        for lineno, raw in enumerate(lines, 1):
            stripped = raw.lstrip()
            if stripped.startswith("```") or stripped.startswith("~~~"):
                in_fence = not in_fence
                continue
            if in_fence:
                continue
            line = strip_code(raw)
            candidates = []
            for pattern in (MD_LINK, MD_IMAGE, AUTOLINK):
                candidates.extend(m.group(1) for m in pattern.finditer(line))
            for m in BARE_URL.finditer(line):
                candidates.append(m.group(0).rstrip(TRAILING_PUNCT))
            for url in candidates:
                url = url.strip().rstrip(TRAILING_PUNCT)
                if is_checkable(url):
                    where = (md, lineno)
                    if where not in found[url]:
                        found[url].append(where)
    return found


def probe(url: str, timeout: float, retries: int, delay: float):
    """Send a GET to the URL with a browser UA and return (classification, detail).

    The classification is "OK", "RESTRICTED", or "DOWN". Network errors, 5xx, and 429 are retried.
    """
    req = urllib.request.Request(
        url,
        method="GET",
        headers={
            "User-Agent": BROWSER_UA,
            "Accept": "*/*",
            "Accept-Language": "ko,en;q=0.8",
        },
    )
    last = ""
    for attempt in range(retries + 1):
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                code = resp.getcode()
                if code in RESTRICTED_CODES:
                    return "RESTRICTED", f"HTTP {code}"
                return "OK", f"HTTP {code}"
        except urllib.error.HTTPError as exc:
            code = exc.code
            if code in RESTRICTED_CODES:
                return "RESTRICTED", f"HTTP {code}"
            if code >= 500 or code == 408:
                last = f"HTTP {code}"  # May be transient, so retry
            else:
                return "DOWN", f"HTTP {code}"  # 404, 410, etc. are definitive; no retry needed
        except (
            urllib.error.URLError,
            http.client.HTTPException,
            ssl.SSLError,
            socket.timeout,
            ConnectionError,
            TimeoutError,
            OSError,
        ) as exc:
            reason = getattr(exc, "reason", exc)
            last = f"{type(exc).__name__}: {reason}"
        if attempt < retries:
            time.sleep(delay * (attempt + 1))
    return "DOWN", last


def main() -> int:
    ap = argparse.ArgumentParser(description="Check that the external links in tracked Markdown are still alive")
    ap.add_argument("--strict", action="store_true",
                    help="exit with code 1 if there is even one DOWN (for periodic/release checks)")
    ap.add_argument("--list", action="store_true",
                    help="print only the URLs to check and their sources, without making network calls")
    ap.add_argument("--timeout", type=float, default=15.0, help="request timeout (seconds)")
    ap.add_argument("--retries", type=int, default=2, help="number of retries for network errors, 5xx, and 429")
    ap.add_argument("--delay", type=float, default=0.5, help="base delay between calls and between retries (seconds)")
    args = ap.parse_args()

    root = subprocess.check_output(
        ["git", "rev-parse", "--show-toplevel"], text=True
    ).strip()
    tracked = subprocess.check_output(["git", "ls-files"], cwd=root, text=True).splitlines()
    md_files = [f for f in tracked if f.endswith(".md")]

    allow = load_allowlist(root)
    found = extract(root, md_files)
    urls = sorted(found)
    print(f"Collected {len(urls)} external URLs to check from {len(md_files)} tracked Markdown files")

    if args.list:
        for url in urls:
            where = ", ".join(f"{f}:{ln}" for f, ln in found[url])
            tag = " [allowlist]" if url in allow else ""
            print(f"  {url}{tag}  ({where})")
        return 0

    results = {"OK": [], "RESTRICTED": [], "ALLOWED": [], "DOWN": []}
    for i, url in enumerate(urls):
        if i:
            time.sleep(args.delay)  # Small delay between calls (to avoid sandbox throttling)
        verdict, detail = probe(url, args.timeout, args.retries, args.delay)
        if verdict == "DOWN" and url in allow:
            verdict = "ALLOWED"  # A known DOWN inherited from upstream that we cannot fix
        results[verdict].append((url, detail))
        print(f"  [{verdict:10}] {url} — {detail}")

    print(
        f"\nSummary: OK {len(results['OK'])} · RESTRICTED {len(results['RESTRICTED'])} · "
        f"ALLOWED {len(results['ALLOWED'])} · DOWN {len(results['DOWN'])}"
    )
    if results["RESTRICTED"]:
        print("RESTRICTED (host alive, checking method blocked; not a broken link):")
        for url, detail in results["RESTRICTED"]:
            print(f"  {url} — {detail}")
    if results["ALLOWED"]:
        print("ALLOWED (known DOWN listed in the allowlist; we cannot fix it, so excluded from strict):")
        for url, detail in results["ALLOWED"]:
            where = ", ".join(f"{f}:{ln}" for f, ln in found[url])
            print(f"  {url} — {detail}  (referenced at: {where})")
    if results["DOWN"]:
        print("DOWN (likely broken; needs checking):")
        for url, detail in results["DOWN"]:
            where = ", ".join(f"{f}:{ln}" for f, ln in found[url])
            print(f"  {url} — {detail}  (referenced at: {where})")

    if args.strict and results["DOWN"]:
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
