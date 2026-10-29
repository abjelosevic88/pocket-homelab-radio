# Configuration

Environment variables; every one has a default. Personal settings (country, sleep timer, notifications, your music library, Pocket Music, AI) live in the UI per person — the `OPENAI_*`/`NOTIFY_*` values below are copied once into the first admin's settings.

| Variable | Default | What |
|---|---|---|
| `PORT`, `HOST` | `8080`, `0.0.0.0` | Listen address |
| `BASE_URL` | – | Public URL (links in pushes, the OIDC callback) |
| `DATA_DIR` | `./data` | Database, Core token, backups |
| `DB_PATH` | `$DATA_DIR/pocket-radio.db` | SQLite file |
| `MAX_BODY_BYTES` | `67108864` | Largest JSON request, e.g. an export being imported (64 MB) |
| `TZ` | – | The zone listening days and hours are counted in |
| `RADIO_DIRECTORY_URL` | `https://de1.api.radio-browser.info` | The station directory ([radio-browser.info](https://www.radio-browser.info), the free community list of ~50,000 stations; any of its mirrors). `off` = stations are added by their address only |
| `ALLOW_LOCAL_STREAMS` | `false` | The server fetches stations, playlist files and logos for people; by default it refuses private, loopback, link-local and CGNAT (Tailscale) addresses so nobody can use it to reach the box's own network. `true` allows an Icecast at home |
| `AUTH_MODE` | `local` | `local` (accounts), `proxy` (trust `AUTH_PROXY_HEADER`/`AUTH_PROXY_EMAIL_HEADER`), `none` |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD`, `ADMIN_NAME` | – | Seed the first admin while the database is empty |
| `SESSION_TTL`, `SECURE_COOKIES` | `720h`, `false` | Set `SECURE_COOKIES=true` behind HTTPS |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_NAME`, `OIDC_AUTO_CREATE` | – | Sign in with an OpenID Connect provider (not needed with Pocket Core) |
| `NOTIFY_URL`, `NOTIFY_FORMAT` | – | Apprise (`…/notify/key`), ntfy topic URL or JSON webhook; format `apprise`/`ntfy`/`json` |
| `OPENAI_API_KEY`, `OPENAI_MODEL`, `OPENAI_BASE_URL` | – | "Describe a channel" and questions without Pocket AI |
| `CORE_URL`, `CORE_REGISTRATION_KEY`, `CORE_APP_URL` | – | Pocket Core |
| `TRUST_PROXY`, `METRICS_ENABLED`, `LOG_LEVEL`, `LOG_FORMAT` | `true`, `true`, `info`, `text` | |
| `DEMO_DATA` | `false` | Seed ten stations, three channels and four months of listening into the owner account (screenshots, trying it out); nothing is fetched or checked |

Commands: `pocket-radio backup [-o FILE]`, `pocket-radio reset-password [-email] [-password]`, `pocket-radio version`.

Data in `DATA_DIR`: `pocket-radio.db`, `logos/` (station logos fetched once and served from here; safe to delete), `backups/`, `core.token*`.

Channels need MP3 transcoding in Navidrome (on by default: it uses ffmpeg). A channel streams at its own bitrate (96–320 kbit/s).
