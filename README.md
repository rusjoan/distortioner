# Distortioner
![Distorted cat](avatar.jpg)

Telegram bot that distorts pictures, stickers, GIFs, videos, video notes, voice messages and text
using Content Aware Scale (seam carving).

## Features

**For users**
- Photos, static and video stickers, GIFs, videos, video notes, voice messages, audio files,
  pictures and videos sent as files, albums, text (`hElLo WoRlD`), captions.
- In groups: reply to a message with `/distort`, optionally with a strength: `/distort 70` (20–80).
- `🔁 More` under every result distorts it once again.
- Queue position with an ETA, live progress for videos and a `❌ Cancel` button.
- `/settings` per chat: default strength, whether to distort sound in videos, language (ru/en).
  In groups only chat admins can change them.

**For admins** — `/admin` opens a panel that works with buttons only:
- 📊 statistics for 24 h / week / month: requests, chats, groups, failures, rejections,
  average processing time, breakdown by media type;
- 📥 queue: waiting and running jobs per chat, cancel all jobs of a chat;
- 🛠 maintenance mode with a custom message;
- 🚫 bans and ⭐ priority chats (skip queue limits), stored in the database;
- ⚙️ limits editable at runtime: file size (up to the Bot API server's limit), video length, jobs per chat, queue size, rate limit, default strength;
- ❗ recent errors; with `DISTORTIONER_KEEP_FAILED_INPUTS=true` the failed input can be downloaded;
- batched failure alerts to admins, at most one every 10 minutes.

Prometheus metrics are served on `/metrics`, health checks on `/healthz` and `/readyz`
(`DISTORTIONER_LISTEN_ADDR`, `:8080` by default).

## Running

1. Create a bot with [@BotFather](https://t.me/BotFather).
2. Copy `.env.example` to `.env`, fill in `DISTORTIONER_BOT_TOKEN` and `DISTORTIONER_ADMIN_IDS`.
3. Start it:

```bash
docker compose up -d
```

The image includes ffmpeg and ImageMagick 7 with liquid rescale. To run the binary directly you need
both on `PATH` (`magick` must support `-liquid-rescale`, i.e. be built with liblqr).

Statistics and settings live in SQLite in `DISTORTIONER_DATA_DIR` (`/app/data` in the image).
A database created by v1 is picked up and its statistics are imported on first start.

All options are listed in [.env.example](.env.example).

### Self-hosted Bot API server

The public Bot API does not let bots download files over 20 MB. `compose.yaml` has an optional
[telegram-bot-api](https://github.com/tdlib/telegram-bot-api) server (profile `local-api`, `--local` mode)
that lifts the limit to 2000 MB:

1. Get `api_id` and `api_hash` at [my.telegram.org](https://my.telegram.org) → API development tools,
   put them into `.env` as `TELEGRAM_API_ID` and `TELEGRAM_API_HASH`.
2. Log the bot out of the cloud server (Telegram requires it; the cloud server is then unavailable
   to the bot for 10 minutes):
   ```bash
   docker compose run --rm --no-deps distortioner logout-cloud
   ```
3. Set `DISTORTIONER_API_URL=http://telegram-bot-api:8081` in `.env` and start both:
   ```bash
   docker compose --profile local-api up -d
   ```
4. Raise the limit in `/admin` → ⚙️ Limits (or `DISTORTIONER_MAX_FILE_MB`).

The bot reads downloaded files directly from the server's volume. The server never deletes them,
so every 15 minutes the bot deletes media older than `DISTORTIONER_BOT_API_FILES_TTL` (3 hours)
from `DISTORTIONER_BOT_API_DIR`; the server's own state is left alone.

### Webhook mode

Set `DISTORTIONER_MODE=webhook`, `DISTORTIONER_WEBHOOK_URL` (the public URL; its path is served by the bot)
and preferably `DISTORTIONER_WEBHOOK_SECRET`. Put a TLS-terminating proxy in front of `DISTORTIONER_LISTEN_ADDR`.

## Development

```bash
make test    # unit tests with the race detector
make lint    # golangci-lint
make itest   # integration tests with real ffmpeg/ImageMagick, in Docker
make run     # run locally with variables from .env
```

Releases: `make release VERSION=1.2.3` checks that `master` is clean and in sync, runs lint and tests,
then tags `v1.2.3` and pushes the tag. GitHub Actions builds an amd64 image
(`ghcr.io/<owner>/distortioner:1.2.3`, `:1.2`, `:latest`) and attaches linux/amd64 and linux/arm64 binaries
to a GitHub release.

## How it works

Images are shrunk to at most 512 px, seam-carved by `strength`% with ImageMagick's `-liquid-rescale`
and scaled back up. Videos are split into frames (at most 30 fps, at most 512 px), every frame is
distorted the same way in parallel, and the frames are encoded back together with the sound
(with a vibrato effect) in a single ffmpeg pass.

Jobs go to two pools — videos and everything else — that serve chats round-robin, so one chat
cannot hold the queue. One semaphore limits concurrent ImageMagick processes to the number of CPUs
across all jobs.

## What gets collected

The bot only stores chat ids with the time and type of each request (for statistics, deleted after
`DISTORTIONER_STATS_RETENTION_DAYS`) and per-chat settings. Media is deleted right after processing;
failed inputs are kept only if `DISTORTIONER_KEEP_FAILED_INPUTS` is enabled, and are deleted after
`DISTORTIONER_FAILED_TTL`.

## Credits

Originally written by [graynk](https://github.com/graynk/distortioner).
