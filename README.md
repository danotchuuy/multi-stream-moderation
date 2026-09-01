# multi-stream-moderation
Locally run application for moderating chat from multiple streams in a single place.

## Stack

- **Backend**: Go (`cmd/server`, `internal/server`) — plain `net/http`, no framework.
- **UI**: Node.js / React / TypeScript via Vite (`web/`).

## Getting started

1. Create a Twitch app at https://dev.twitch.tv/console/apps to get a
   Client ID and Client Secret. Add
   `http://localhost:8080/api/auth/twitch/callback` as an OAuth Redirect
   URL.
2. Export the credentials before running the server:

   ```sh
   export TWITCH_CLIENT_ID=...
   export TWITCH_CLIENT_SECRET=...
   ```

```sh
make install   # npm install for the UI
make dev       # runs the Go API (:8080) and the Vite dev server (:5173) together
```

Open **http://localhost:5173** (not `127.0.0.1` — the Twitch OAuth
callback lands on `:8080`, and the session cookie is only shared back to
the UI dev server because both share the `localhost` hostname) — the UI
dev server proxies `/api/*` requests to the Go server.

Click "Connect Twitch account" to authorize, then pick a channel you
moderate (or type any channel name) to watch its chat live. Each message
from a channel you moderate shows Delete / Timeout / Ban controls, unless
it's from the broadcaster or (for a regular moderator) another moderator
— matching what Twitch itself allows.

> If you connected before moderation actions were added, sign out and
> reconnect once — the delete/ban/timeout scopes weren't requested by
> earlier sessions, so those calls will 401 until you do.

### Kick

Kick chat lives in its own panel next to Twitch's, with the same Delete /
Timeout / Ban controls (Kick's own moderation rules apply the same way:
no acting on the broadcaster, and a regular moderator can't act on
another moderator). It works differently under the hood, though, because
Kick's official API has no endpoint for *reading* chat — only sending,
deleting, and moderating. Live messages only arrive via a **webhook**,
which means:

- This server's `/api/kick/webhook` endpoint must be reachable from the
  public internet so Kick can deliver events to it. For local dev, run a
  tunnel (e.g. `ngrok http 8080` or `cloudflared tunnel --url
  http://localhost:8080`) and use the HTTPS URL it gives you.
- Signing in to Kick is required just to *watch* chat, unlike Twitch's
  anonymous read — subscribing to a channel's events needs your own
  Kick OAuth token.
- There's no "channels I moderate" listing (Kick's API doesn't expose
  one), so the channel picker only offers your own channel as a
  one-click pick; anything else is typed in manually.

Below the Kick chat panel, a **Repeated messages** panel appears
whenever 2+ different chatters send the exact same message (trimmed,
case-insensitive) within a 5-minute window — a common raid/copypasta
pattern — with Delete all / Timeout all / Ban all buttons that act on
every message or sender in that group at once. It's computed entirely
client-side from the messages already streaming into the panel, so it
only sees spam that happens while you're watching, and it never groups
the broadcaster's messages or (for a regular moderator) other
moderators'.

Setup:

1. Create a Kick app at https://kick.com/settings/developer. Add
   `http://localhost:8080/api/auth/kick/callback` as its redirect URL.
2. In the same app's settings, turn on "Enable Webhooks" and set the
   webhook URL to `<your tunnel's HTTPS URL>/api/kick/webhook`.
3. Export the credentials:

   ```sh
   export KICK_CLIENT_ID=...
   export KICK_CLIENT_SECRET=...
   ```

If you skip the tunnel/webhook setup, Twitch still works fully — Kick's
panel just won't receive any messages (the "Connect Kick account" step
still works either way).

### YouTube

YouTube live chat gets its own third panel, with the same Delete /
Timeout / Ban controls (no acting on the broadcaster, and a regular
moderator can't act on another moderator). A few things work differently
here too:

- A YouTube **channel** doesn't have one persistent chat the way a
  Twitch/Kick channel does — each individual broadcast has its own. So
  instead of picking a channel, you either watch your own current live
  stream (one click, if you're live) or paste a live stream's URL or
  video ID.
- YouTube's Data API has no push mechanism for chat (no IRC, no
  webhooks) — this app polls `liveChatMessages.list` server-side on your
  behalf and relays new messages over the same SSE mechanism the other
  two panels use. It also means, unlike Twitch's CLEARMSG or Kick's ban
  webhook, this app gets no signal when a message is deleted or a user
  banned from elsewhere — the panel only reflects moderation actions you
  take from it.
- Signing in is required just to watch, like Kick (there's no anonymous
  read).
- The default free quota is 10,000 units/day; each poll costs ~1 unit
  and this app polls every 2-15 seconds (YouTube tells it the interval),
  so normal use for a personal moderation tool comfortably fits.

Setup:

1. In the [Google Cloud Console](https://console.cloud.google.com/),
   create a project (or use an existing one), enable the **YouTube Data
   API v3**, and create an **OAuth client ID** (Web application) under
   *APIs & Services → Credentials*. Add
   `http://localhost:8080/api/auth/youtube/callback` as an authorized
   redirect URI.
2. On the *OAuth consent screen*, add your own Google account as a test
   user (unless/until you publish the app) — otherwise Google will
   refuse to let it sign you in.
3. Export the credentials:

   ```sh
   export YOUTUBE_CLIENT_ID=...
   export YOUTUBE_CLIENT_SECRET=...
   ```

Twitch and Kick both work fully without any of this — YouTube's panel
just shows "Connect YouTube account" until it's configured.

### Staying signed in across restarts

Once authorized, the server persists your session (including the Twitch
access/refresh tokens), encrypted, to a file — by default
`sessions.enc` under your OS user config directory (e.g.
`~/.config/multi-stream-moderation/` on Linux). Restarting `make dev` /
`make run` reloads it, refreshing the access token automatically if it
has expired, so you won't be sent back through the Twitch login each
time. Override the location with `SESSION_STORE_FILE`, or delete the
file (or hit "Sign out" in the UI) to force a fresh login. Kick and
YouTube sessions work the same way, each in their own file
(`kick-sessions.enc` / `youtube-sessions.enc` by default, override with
`KICK_SESSION_STORE_FILE` / `YOUTUBE_SESSION_STORE_FILE`) and their own
cookie, so signing out of one platform doesn't affect the others.

## Security: BFF (Backend-For-Frontend) token handling

The browser never sees a Twitch, Kick, or YouTube access/refresh token —
it only ever holds an opaque session cookie (one per platform), and
every API call to those platforms (including OAuth token
exchange/refresh and moderation actions) happens inside
`internal/server`, using the client secret and tokens that stay
server-side. This is the standard BFF pattern for OAuth in a browser
app, and it's what makes each of the following meaningful:

- **Session cookie**: `HttpOnly` (JavaScript, and so XSS, can't read
  it), `SameSite=Lax` (not sent on cross-site requests, which covers
  CSRF for the state-changing endpoints), and `Secure` whenever the
  server is configured for HTTPS (auto-detected from
  `TWITCH_REDIRECT_URL`, or set explicitly with `COOKIE_SECURE`).
- **At-rest encryption**: the session store (see above) is AES-256-GCM
  encrypted, not plain JSON. The key lives in a separate file
  (`session.key`, `0600`, generated on first run) next to the store, so
  a leak of just the session file — e.g. it ending up in a broader
  backup or sync location that the key file doesn't — isn't enough on
  its own to recover a token. Set `SESSION_ENCRYPTION_KEY` (a
  base64-encoded 32-byte key) to supply your own instead. This protects
  against the file being read out from under the running server; it's
  not a substitute for restricting who can access the machine at all —
  an attacker with control of the process (or root) can always reach
  the keys the process itself holds in memory.
- **CORS**: closed by default (no `Access-Control-Allow-Origin` header
  at all). The documented dev setup (Vite's proxy) and the production
  setup (this server serving the built UI) both reach the API
  same-origin, where CORS doesn't apply, so there's normally nothing to
  configure. Only set `ALLOWED_ORIGIN` if you genuinely serve the UI
  from a different origin than this server.

## Production build

```sh
make build     # builds web/dist, then bin/server
make run       # builds and runs bin/server, which serves web/dist directly on :8080
```

## Docker

The `Dockerfile` builds a single multi-arch image (Go API + built UI,
served as one binary) using [`docker buildx`](https://docs.docker.com/build/building/multi-platform/) —
a plain `docker build` can't produce it, since the Dockerfile relies on
buildx-only features (`--platform=$BUILDPLATFORM` cross-compilation,
build cache mounts). The Makefile targets below always invoke
`docker buildx build` for you.

```sh
make docker-build-local   # build for your machine only, load into Docker to test
make docker-build         # validate the multi-arch build (linux/amd64 + linux/arm64)
make docker-push          # build multi-arch and push to Docker Hub (docker login first)
```

Each one applies two tags in the same build (buildx builds once, tags
twice — not two separate builds): the moving `DOCKER_TAG` below, and an
immutable, date-coded `DOCKER_DATE_TAG` (`YYYY.MM.DD`, UTC), so you can
always pin to a specific day's build even after `latest` moves on.

All three accept overrides, e.g.:

```sh
make docker-push DOCKER_IMAGE=youruser/multi-stream-moderation DOCKER_TAG=v1.0.0
```

| Variable           | Default                              |
| ------------------ | ------------------------------------- |
| `DOCKER_IMAGE`      | `changeme/multi-stream-moderation` — set this to your own Docker Hub repo |
| `DOCKER_TAG`        | `latest` |
| `DOCKER_DATE_TAG`   | today's date, e.g. `2026.09.01` — override if you need a second same-day build to get its own tag |
| `DOCKER_PLATFORMS`  | `linux/amd64,linux/arm64` |

A multi-arch result can't be loaded into your local Docker image store
(only pushed, or built one platform at a time) — `docker-build` only
proves the build succeeds on every platform; use `docker-build-local` to
actually run one.

The image runs as a non-root user on a minimal (`distroless`) base and
reads the same environment variables documented above (`TWITCH_CLIENT_ID`,
`KICK_CLIENT_ID`, `YOUTUBE_CLIENT_ID`, etc. — see `docker run --env-file`).
Session stores default to `/data` inside the container; mount a volume
there to keep users signed in across container restarts:

```sh
docker run -p 8080:8080 --env-file .env -v msm-data:/data changeme/multi-stream-moderation
```

## Layout

```
cmd/server/        entrypoint
internal/server/    HTTP routes and handlers: Twitch OAuth/chat/moderation (auth.go,
                     twitch_*.go, handlers_*.go), Kick OAuth/moderation/webhooks
                     (kick_*.go, handlers_kick.go), and YouTube OAuth/moderation/
                     polling (youtube_*.go, handlers_youtube.go)
internal/twitch/     Twitch chat (IRC) client
web/                 Vite + React + TypeScript UI (Twitch, Kick, and YouTube
                     components/lib are separate; App.tsx renders them as three
                     independent panels)
Dockerfile           multi-arch (buildx) image: builds web/dist and the Go binary,
                     runs them as one non-root distroless container
```
