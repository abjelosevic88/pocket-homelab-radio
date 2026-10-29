CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'member',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, setup flag); everything else is personal.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

CREATE TABLE cache (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  fetched_at TEXT NOT NULL
);


-- An internet radio station a person keeps: a favourite, or one they
-- played once (favorite = 0, kept for "recently played"). url is what was
-- added; url_resolved is the real stream after .pls/.m3u files and
-- redirects. health is the daily check of favourites.
CREATE TABLE stations (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name           TEXT NOT NULL,
  url            TEXT NOT NULL,
  url_resolved   TEXT NOT NULL DEFAULT '',
  homepage       TEXT NOT NULL DEFAULT '',
  logo_url       TEXT NOT NULL DEFAULT '',
  tags           TEXT NOT NULL DEFAULT '',
  country_code   TEXT NOT NULL DEFAULT '',
  language       TEXT NOT NULL DEFAULT '',
  codec          TEXT NOT NULL DEFAULT '',
  bitrate        INTEGER NOT NULL DEFAULT 0,
  hls            INTEGER NOT NULL DEFAULT 0,
  directory_id   TEXT NOT NULL DEFAULT '',        -- radio-browser stationuuid
  favorite       INTEGER NOT NULL DEFAULT 1,
  position       INTEGER NOT NULL DEFAULT 0,
  notes          TEXT NOT NULL DEFAULT '',
  source         TEXT NOT NULL DEFAULT 'manual',  -- manual | directory | import | navidrome | <pocket app slug>
  play_count     INTEGER NOT NULL DEFAULT 0,
  last_played_at TEXT,
  health         TEXT NOT NULL DEFAULT 'unknown', -- unknown | ok | failing
  fail_count     INTEGER NOT NULL DEFAULT 0,
  checked_at     TEXT,
  health_note    TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX stations_user_url ON stations(user_id, url);
CREATE INDEX stations_user_fav ON stations(user_id, favorite, position);
CREATE INDEX stations_user_played ON stations(user_id, last_played_at);

-- A station of a person's own, played from their music library (Navidrome):
-- the songs come from the rules; listen_token is the secret in the stream
-- address other players use (/listen/<token>.mp3).
CREATE TABLE channels (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name           TEXT NOT NULL,
  description    TEXT NOT NULL DEFAULT '',
  rules          TEXT NOT NULL DEFAULT '{}',      -- JSON, see store.ChannelRules
  color          TEXT NOT NULL DEFAULT '',
  bitrate        INTEGER NOT NULL DEFAULT 192,
  listen_token   TEXT NOT NULL UNIQUE,
  last_played_at TEXT,
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX channels_user_name ON channels(user_id, name);

-- Songs a channel played, so it does not repeat them too soon.
CREATE TABLE channel_plays (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  song_id    TEXT NOT NULL,
  artist     TEXT NOT NULL DEFAULT '',
  played_at  TEXT NOT NULL
);
CREATE INDEX channel_plays_channel ON channel_plays(user_id, channel_id, played_at);

-- One stretch of listening: a station or a channel, from start to end.
CREATE TABLE listens (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  station_id INTEGER REFERENCES stations(id) ON DELETE SET NULL,
  channel_id INTEGER REFERENCES channels(id) ON DELETE SET NULL,
  name       TEXT NOT NULL DEFAULT '',
  client     TEXT NOT NULL DEFAULT 'web',        -- web | app (another player) | import
  started_at TEXT NOT NULL,
  ended_at   TEXT NOT NULL,
  seconds    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX listens_user_started ON listens(user_id, started_at);

-- A song heard on a station (from the stream's titles) or played on a
-- channel. saved = kept in "Saved songs".
CREATE TABLE heard (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  station_id INTEGER REFERENCES stations(id) ON DELETE SET NULL,
  channel_id INTEGER REFERENCES channels(id) ON DELETE SET NULL,
  source     TEXT NOT NULL DEFAULT '',           -- the station or channel name at the time
  title      TEXT NOT NULL,                      -- as the station sent it
  artist     TEXT NOT NULL DEFAULT '',
  song       TEXT NOT NULL DEFAULT '',
  song_id    TEXT NOT NULL DEFAULT '',           -- Navidrome song id (channels)
  saved      INTEGER NOT NULL DEFAULT 0,
  heard_at   TEXT NOT NULL
);
CREATE INDEX heard_user_at ON heard(user_id, heard_at);
CREATE INDEX heard_user_saved ON heard(user_id, saved, heard_at);
