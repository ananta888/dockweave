#!/usr/bin/env python3
"""Check external HTTP(S) links without third-party dependencies."""

from __future__ import annotations

import argparse
import concurrent.futures
import re
import ssl
import sys
import time
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import Request, urlopen


URL_RE = re.compile(r"https?://[^\s<>)\]\"']+")
RESTRICTED_STATUSES = {401, 403, 429}
RESERVED_EXAMPLE_SUFFIXES = (".example", ".example.com", ".example.net", ".example.org", ".invalid")


def is_reserved_example(url: str) -> bool:
    hostname = (urlsplit(url).hostname or "").lower()
    return hostname in {"example", "example.com", "example.net", "example.org", "localhost"} or hostname.endswith(
        RESERVED_EXAMPLE_SUFFIXES
    )


def collect_urls(root: Path) -> list[str]:
    urls: set[str] = set()
    for pattern in ("*.md", "*.yaml", "*.yml"):
        for path in root.rglob(pattern):
            text = path.read_text(encoding="utf-8")
            for raw_url in URL_RE.findall(text):
                url = raw_url.rstrip(".,;:")
                if not is_reserved_example(url):
                    urls.add(url)
    return sorted(urls)


def request_once(url: str, timeout: float, use_range: bool) -> tuple[int, str]:
    headers = {
        "Accept": "text/html,application/xhtml+xml,application/pdf,*/*;q=0.8",
        "User-Agent": "keycloak-oidc-human-guide-link-check/1.0",
    }
    if use_range:
        headers["Range"] = "bytes=0-1023"
    request = Request(url, headers=headers, method="GET")
    with urlopen(request, timeout=timeout, context=ssl.create_default_context()) as response:
        response.read(1)
        return response.status, response.geturl()


def check_url(url: str, timeout: float) -> tuple[str, str, str]:
    last_error = "unknown error"
    for attempt in range(2):
        try:
            status, final_url = request_once(url, timeout, use_range=attempt == 0)
            if 200 <= status < 400:
                original_host = (urlsplit(url).hostname or "").lower()
                final_host = (urlsplit(final_url).hostname or "").lower()
                if final_host and final_host != original_host:
                    return "WARN", url, f"HTTP {status}; redirected to different host: {final_host}"
                suffix = "" if final_url == url else f" -> {final_url}"
                return "OK", url, f"HTTP {status}{suffix}"
            last_error = f"HTTP {status}"
        except HTTPError as exc:
            if exc.code in RESTRICTED_STATUSES:
                return "WARN", url, f"HTTP {exc.code}; endpoint exists but restricts automated checks"
            if exc.code == 416 and attempt == 0:
                continue
            last_error = f"HTTP {exc.code}: {exc.reason}"
        except (URLError, TimeoutError, ssl.SSLError, OSError) as exc:
            last_error = str(exc)
        if attempt == 0:
            time.sleep(0.2)
    return "FAIL", url, last_error


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("root", nargs="?", default=".", help="repository root")
    parser.add_argument("--timeout", type=float, default=20.0, help="seconds per request")
    parser.add_argument("--workers", type=int, default=8, help="parallel requests")
    args = parser.parse_args()

    root = Path(args.root).resolve()
    urls = collect_urls(root)
    if not urls:
        print("No external links found.")
        return 0

    with concurrent.futures.ThreadPoolExecutor(max_workers=max(1, args.workers)) as executor:
        results = list(executor.map(lambda url: check_url(url, args.timeout), urls))

    failures = 0
    warnings = 0
    for status, url, detail in sorted(results, key=lambda item: item[1]):
        print(f"{status}: {url} ({detail})")
        failures += status == "FAIL"
        warnings += status == "WARN"

    print(f"Checked {len(urls)} link(s): {failures} failure(s), {warnings} restricted warning(s).")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
