#!/usr/bin/env python3
"""
Browser-Use agent that connects to the kernel Chrome via CDP and executes a task: join the meeting URL, set name to KERNELAI, and present the virtual feed tab.
Fails due to:
  - DOM context limitations which restrict permissions
  - Browseruse agents inject stuff that makes the meet ui behave oddly
replaced with Moondream agent
"""
import argparse
import json
import asyncio
import os
import sys
import urllib.request
from urllib.parse import urlparse, urlunparse
from browser_use import Agent, Browser, ChatOpenAI
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


async def ensure_permissions_and_seed(cdp_url: str, feed_url: str, meeting_url: str) -> None:
    """Grant mic/display permissions via CDP and pre-open feed + meeting tabs."""
    async with async_playwright() as p:
        browser = await p.chromium.connect_over_cdp(cdp_url)
        context = browser.contexts[0] if browser.contexts else await browser.new_context()
        context.on("dialog", lambda dialog: asyncio.create_task(dialog.accept()))
        origin = f"{urlparse(meeting_url).scheme}://{urlparse(meeting_url).netloc}"
        try:
            await context.grant_permissions(["microphone", "display-capture"], origin=origin)
        except Exception:
            pass
        # Seed feed tab
        feed_page = await context.new_page()
        await feed_page.goto(feed_url)
        await feed_page.evaluate("document.title = 'Virtual Input Feed'")
        await feed_page.wait_for_load_state("domcontentloaded")
        # Meeting tab
        meeting_page = await context.new_page()
        await meeting_page.goto(meeting_url)
        await meeting_page.bring_to_front()
        # Leave browser running; disconnect Playwright


async def main() -> None:
    load_dotenv()

    parser = argparse.ArgumentParser()
    parser.add_argument("--meeting_url", required=True)
    parser.add_argument(
        "--task",
        help="Override task text (default: join meeting and share feed tab)",
    )
    args = parser.parse_args()

    meeting_url = args.meeting_url.strip()
    kernel_base = os.environ.get("KERNEL_API_BASE", "http://localhost:444").rstrip("/")
    feed_url = feed_url_for_browser(kernel_base)

    if not os.environ.get("OPENAI_API_KEY"):
        raise RuntimeError("OPENAI_API_KEY is required in environment")

    cdp_url = resolve_cdp_url()
    await ensure_permissions_and_seed(cdp_url, feed_url, meeting_url)
    llm = ChatOpenAI(model="gpt-4o")
    browser = Browser(
        cdp_url=cdp_url,
        permissions=["microphone", "display-capture", "clipboardReadWrite", "notifications"],
    )

    if args.task:
        task = args.task
    else:
        task = (
            "Use the existing browser via CDP and do exactly this: "
            f"- In one tab, open the virtual feed: {feed_url}. "
            f"- In a new tab, open the meeting: {meeting_url}. "
            "- When the browser/meet prompts for permissions, explicitly allow microphone access and confirm permissions dialogs. "
            '- Set the display name to "KERNELAI" before joining. '
            "- Join the meeting (Join/Ask to join as needed). "
            "- Click Present/Share. It should immediately say that you started presenting."
            "- After you are in the meeting (and/or after presenting), double-check the microphone is active; if not, refresh, click the microphone button (it can be red button or black button or any color button with a mic icon ; but everytime after you refresh, press the mic icon buttons to see what comes up in any case) to trigger the permission prompt dialogs. refresh the meeting tab if needed when you see mic not enabled, try to allow mic using the described procedure (retry a couple times if needed), and rejoin."
            "If you see that you started presenting, even if the screen of your presentation is black/blank, it means your task is completed successfully."
        )

    agent = Agent(task=task, browser=browser, llm=llm)
    await agent.run()


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except Exception as exc:  # noqa: BLE001
        print(f"[browser_join] failed: {exc}", file=sys.stderr)
        sys.exit(1)
