#!/usr/bin/env python3
"""
Simple script to open virtual feed and meeting tabs via Playwright CDP connection,
intended for manual debugging workflows.
"""
import argparse
import json
import asyncio
import os
import sys
import urllib.request
from urllib.parse import urlparse, urlunparse
from dotenv import load_dotenv
from playwright.async_api import async_playwright


def resolve_cdp_url() -> str:
    candidates = [
        "http://localhost:9222/json/version",
        "http://127.0.0.1:9222/json/version",
    ]
    last_err: Exception | None = None
    for url in candidates:
        try:
            parsed = urlparse(url)
            host_header = parsed.netloc or "localhost:9222"
            req = urllib.request.Request(url, headers={"Host": host_header})
            with urllib.request.urlopen(req, timeout=5) as resp:
                data = json.loads(resp.read().decode())
            ws = (
                data.get("webSocketDebuggerUrl")
                or data.get("websocketDebuggerUrl")
                or data.get("webSocketDebuggerURL")
            )
            if ws:
                ws_parsed = urlparse(ws)
                if ws_parsed.netloc in ("localhost", "127.0.0.1", ""):
                    port = ws_parsed.port or parsed.port or 9222
                    netloc = f"{ws_parsed.hostname or 'localhost'}:{port}"
                    ws = urlunparse(ws_parsed._replace(netloc=netloc))
                return ws
            if isinstance(data, list) and data and isinstance(data[0], dict):
                ws = data[0].get("webSocketDebuggerUrl")
                if ws:
                    ws_parsed = urlparse(ws)
                    if ws_parsed.netloc in ("localhost", "127.0.0.1", ""):
                        port = ws_parsed.port or parsed.port or 9222
                        netloc = f"{ws_parsed.hostname or 'localhost'}:{port}"
                        ws = urlunparse(ws_parsed._replace(netloc=netloc))
                    return ws
            last_err = RuntimeError(f"No webSocketDebuggerUrl in response from {url}")
        except Exception as exc:  # noqa: BLE001
            last_err = exc
    raise last_err or RuntimeError("Failed to resolve CDP URL")


def feed_url_for_browser(kernel_base: str) -> str:
    """Kernel API is reachable on 444 from host; inside Chrome use 10001."""
    override = os.environ.get("BROWSER_KERNEL_API_BASE")
    base = override or kernel_base
    parsed = urlparse(base)
    host = parsed.hostname or "localhost"
    port = parsed.port or 444
    if port == 444:
        port = 10001
    netloc = f"{host}:{port}"
    browser_base = urlunparse(parsed._replace(netloc=netloc))
    if browser_base.endswith("/"):
        browser_base = browser_base[:-1]
    return f"{browser_base}/input/devices/virtual/feed?fit=cover"


async def open_tabs(cdp_url: str, feed_url: str, meeting_url: str) -> None:
    """Grant mic/display permissions via CDP and open feed + meeting tabs."""
    async with async_playwright() as p:
        print(f"Connecting to CDP: {cdp_url}")
        browser = await p.chromium.connect_over_cdp(cdp_url)
        context = browser.contexts[0] if browser.contexts else await browser.new_context()
        context.on("dialog", lambda dialog: asyncio.create_task(dialog.accept()))
        
        origin = f"{urlparse(meeting_url).scheme}://{urlparse(meeting_url).netloc}"
        try:
            print(f"Granting permissions for {origin}")
            await context.grant_permissions(["microphone", "display-capture"], origin=origin)
        except Exception as e:
            print(f"Permission grant warning (may be redundant): {e}")

        # Seed feed tab
        print(f"Opening feed tab: {feed_url}")
        feed_page = await context.new_page()
        await feed_page.goto(feed_url)
        await feed_page.evaluate("document.title = 'Virtual Input Feed'")
        await feed_page.wait_for_load_state("domcontentloaded")
        
        # Meeting tab
        print(f"Opening meeting tab: {meeting_url}")
        meeting_page = await context.new_page()
        await meeting_page.goto(meeting_url)
        await meeting_page.bring_to_front()
        
        print("Tabs opened. Disconnecting Playwright (browser stays open).")
        # Leave browser running; disconnect Playwright


async def main() -> None:
    load_dotenv()

    parser = argparse.ArgumentParser()
    parser.add_argument("--meeting_url", required=True)
    args = parser.parse_args()

    meeting_url = args.meeting_url.strip()
    kernel_base = os.environ.get("KERNEL_API_BASE", "http://localhost:444").rstrip("/")
    feed_url = feed_url_for_browser(kernel_base)

    try:
        cdp_url = resolve_cdp_url()
        await open_tabs(cdp_url, feed_url, meeting_url)
    except Exception as e:
        print(f"Error opening tabs: {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    asyncio.run(main())
