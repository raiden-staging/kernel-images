# Agent Live Conductor

Kernel virtual inputs/livestream + ElevenLabs agent + remote browser automation : Google meet demo

## Usage (full sequence)

Setup `.env` vars

```env
ELEVENLABS_AGENT_ID=xxxxxxxxxxxxxxxxxxxx # enable override system prompt + first message
MOONDREAM_API_KEY=xxxxxxxxxxxxxxxxxxxx
# REMOTE_RTMP_URL=rtmps://xxxxxxxxxxxxxxxxxxxx # (optional) use this + enable ENABLE_REMOTE_LIVESTREAM to record the session (audio+video) to remote RTMP additionally
```

Run the sequence (includes all installs) :

```bash
bun i
sudo chmod +x sequence.sh
./sequence.sh "https://meet.google.com/xxx-yyyy-zzz"
```