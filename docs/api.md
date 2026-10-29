# API

JSON over HTTP under `/api/v1`. Authenticate with the session cookie (browser), `Authorization: Bearer <token>`
(`pra_…`, made under Settings → API & shortcuts), or a Pocket Core token. Types: `web/src/lib/types.ts`.
Everything is per person: another person's ids answer 404. Times are RFC 3339 UTC; days and hours of the history are
counted in the server's `TZ`.

## Overview

| Call | What |
| :--- | :--- |
| `GET /dashboard` | Favourites, recently played, channels, the last songs heard, minutes today and this week, failing favourites |
| `GET /now?sid=` | What one player session plays (`title`, `artist`, `song`, `title_since`); without `sid` all of the person's streams |
| `GET /history?days=N` or `?from=&to=` | Minutes per day and per hour of the day, the top stations and channels, the listens, heard and saved counts |
| `GET /day/{date}` | One day for Pocket Day: minutes, listens, top three, songs saved that day |
| `POST /listens/beat` `{sid, station_id, stop?}` | Keeps the listen of an HLS station going (the browser plays those itself) |
| `DELETE /listens/{id}` | Remove a listen from the history |

## Stations

| Call | What |
| :--- | :--- |
| `GET /stations?view=favorites\|recent\|all&q=` | Your stations (favourites in your order; recent = played, newest first) |
| `POST /stations` | Add a station: `name url logo_url homepage tags notes favorite`, or the fields of a directory result with `directory_id`. A typed address is opened first and the stream fills in name, genre, codec and bitrate; if it does not play the answer is 422 `{probe_failed}` — send `force: true` to keep it anyway. The same address twice returns the existing station (making it a favourite when `favorite` is true) |
| `GET/PUT/DELETE /stations/{id}` | One station. PUT takes any of the fields above |
| `POST /stations/reorder` `{ids}` | The favourites in this order |
| `POST /stations/{id}/check` | Try it now (the daily check marks it failing after 3 bad days) |
| `GET /stations/export.m3u` | Your favourites as an extended M3U |
| `POST /stations/import` | A `.m3u` / `.pls` file (the body, or `{text}`) → `{added, skipped}` |
| `POST /stations/import/navidrome` | The internet radio stations kept in your Navidrome |
| `GET /logo?u=` | A station logo through the server's cache |
| `GET /directory/search?q=&tag=&country=&language=&order=popular\|votes\|name\|new&offset=&limit=` | radio-browser.info; each result says `saved_id` / `favorite` when you have it |
| `GET /directory/tags` · `GET /directory/countries` | Genres and countries with station counts |

Playing: `GET /stream/station/{id}?sid=…` (outside `/api/v1`, no timeout) relays the stream with the song titles taken
out; the titles go to `/now` and the heard list.

## Channels (stations of your own)

| Call | What |
| :--- | :--- |
| `GET /library` | Your Navidrome's genres (song counts) and playlists, for the editor |
| `GET /library/artists?q=` | Find artists |
| `GET /cover/{id}?size=` | A song's or album's cover |
| `GET /channels` · `POST /channels` | Channels: `name description color bitrate rules{genres exclude_genres artists exclude_artists playlists year_from year_to starred_only avoid_hours artist_gap}`; each comes with `listen_url`, `playlist_url`, `on_air`, `now` |
| `GET/PUT/DELETE /channels/{id}` | One channel |
| `GET /channels/{id}/now` | On air?, the song, listeners, the last songs |
| `POST /channels/{id}/address` | A new listen address (the old one stops) |
| `POST /channels/preview` `{rules}` | A sample of songs and about how many there are |
| `POST /channels/describe` `{text}` | AI drafts name, description and rules from your words and your library's genres (nothing saved) |

Playing: `GET /stream/channel/{id}?sid=…` (signed in) or `GET /listen/{token}.mp3` for any player — one MP3 stream,
ICY titles when the player asks (`Icy-MetaData: 1`). `/listen/{token}.m3u` and `.pls` are playlist files pointing at it.
While anyone listens, one broadcast plays for all of them; it goes off air 20 s after the last one leaves.

## Songs you heard

| Call | What |
| :--- | :--- |
| `GET /heard?saved=1&q=&station_id=&channel_id=&before=&limit=` | Newest first; page with `before` = the last `heard_at` |
| `PUT /heard/{id}` `{saved}` · `DELETE /heard/{id}` | Keep or forget a song |
| `GET /heard/{id}/library` | Is it in your music library (Pocket Music)? `{in_library, song, open_url, search_url}` |
| `POST /heard/{id}/playlist` | Add it to your Pocket Music playlist (Settings → "Saved songs go to") |

## AI

`GET /ai/status`, `GET /ai/models`, `POST /ai/ask` `{question}` (about your listening of the last 90 days).

## Account and box

`GET /auth/status`, `POST /auth/login`, `POST /auth/setup`, `POST /auth/logout`, `GET|PUT /auth/me`, `GET|POST /tokens`,
`DELETE /tokens/{id}`, `GET|PUT /settings`, `GET /settings/system`, `POST /integrations/test` (`which`: `navidrome`,
`music`, `openai`, `notify`), `POST /notify/test`, `GET|POST /users`, `PUT|DELETE /users/{id}` (admins), `GET /backup`
(admins), `GET /export.json`, `GET /export.csv` (listening), `POST /import` (a Pocket Radio export).

`GET /healthz`, `GET /readyz`, `GET /metrics`.

## Pocket Core links

Offers `radio:read` (`/dashboard /now /history /stations /stations/{id} /channels /channels/{id} /heard /day/{date}`)
and `radio:write` (`POST /stations` — adds a favourite, marked with the acting app). Publishes `radio.listened`
(a listen of a minute or more ended), `radio.song_saved`, `radio.station_added`.
Uses Pocket Music (`playlists:write`), the person's Navidrome login (service `navidrome`) and Pocket AI.
