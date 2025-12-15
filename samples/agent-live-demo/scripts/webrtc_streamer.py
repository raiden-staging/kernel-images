#!/usr/bin/env python3
import argparse
import asyncio
from pathlib import Path

import aiohttp
from aiortc import RTCPeerConnection, RTCSessionDescription
from aiortc.contrib.media import MediaPlayer


async def stream(offer_url: str, video_path: Path):
    pc = RTCPeerConnection()
    player = MediaPlayer(video_path.as_posix(), options={"-stream_loop": "-1"})
    if player.video:
        pc.addTrack(player.video)

    offer = await pc.createOffer()
    await pc.setLocalDescription(offer)

    async with aiohttp.ClientSession() as session:
        resp = await session.post(offer_url, json={"sdp": pc.localDescription.sdp})
        answer = await resp.json()

    await pc.setRemoteDescription(RTCSessionDescription(sdp=answer["sdp"], type="answer"))
    print("WebRTC video streaming started", flush=True)
    await asyncio.Future()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--offer-url", required=True, help="Kernel WebRTC offer URL")
    parser.add_argument("--video", required=True, help="Video file to loop")
    args = parser.parse_args()
    video_path = Path(args.video).resolve()
    if not video_path.exists():
        raise SystemExit(f"Video file not found: {video_path}")
    asyncio.run(stream(args.offer_url, video_path))


if __name__ == "__main__":
    main()
