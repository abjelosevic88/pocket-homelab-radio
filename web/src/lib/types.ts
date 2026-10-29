export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface APIToken { id: number; user_id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export type ConnMode = 'core' | 'direct' | 'off'
export type ConnVia = '' | 'core' | 'direct'

export interface Settings {
  core_configured: boolean
  core_registered?: boolean
  title: string; date_format: string; time_format: string; week_start: number

  // listening
  country: string            // two-letter code for "Popular where you live" ('' = none)
  sleep_minutes: number      // the sleep timer's default
  channel_bitrate: number    // new channels stream at this (kbit/s)
  keep_heard_days: number    // unsaved heard songs are forgotten after this (0 = keep)

  notify_broken: boolean; notify_url: string; notify_format: string; notify_configured: boolean

  // your music library (Navidrome) for channels: through a Pocket Core login, or direct
  navidrome_mode: ConnMode; navidrome_url: string; navidrome_user: string; navidrome_password: string

  // Pocket Music: is a heard song in your library, saved songs to a playlist
  music_mode: ConnMode; music_via: ConnVia; music_detail: string; music_open_url: string; music_configured: boolean
  music_url: string; music_token: string; music_public_url: string; music_playlist: string

  ai_provider: 'auto' | 'pocket' | 'openai'; ai_pocket_model: string
  openai_api_key: string; openai_model: string; openai_base_url: string

  setup_complete: boolean; base_url: string
  directory: boolean      // the station directory (radio-browser.info) is on
  local_streams: boolean  // stations on the local network are allowed
}

// ---- stations ----

export type Health = 'unknown' | 'ok' | 'failing'

export interface Station {
  id: number; name: string; url: string; url_resolved: string; homepage: string; logo_url: string
  tags: string; country_code: string; language: string; codec: string; bitrate: number; hls: boolean
  directory_id: string; favorite: boolean; position: number; notes: string; source: string
  play_count: number; last_played_at: string | null
  health: Health; fail_count: number; checked_at: string | null; health_note: string
  created_at: string; updated_at: string
}

/** A station in the directory (radio-browser.info). saved_id = the person already has it. */
export interface DirStation {
  id: string; name: string; url: string; url_resolved: string; homepage: string; logo_url: string
  tags: string; country_code: string; country: string; language: string; codec: string; bitrate: number; hls: boolean
  votes: number; clicks: number; working: boolean; saved_id?: number; favorite?: boolean
}
export interface DirTag { name: string; count: number }
export interface DirCountry { code: string; name: string; count: number }

// ---- channels: stations of your own from the music library ----

export interface ChannelRules {
  genres: string[]; exclude_genres: string[]; artists: string[]; exclude_artists: string[]; playlists: string[]
  year_from: number; year_to: number; starred_only: boolean
  avoid_hours: number   // no song again within this many hours
  artist_gap: number    // songs between two by the same artist
}

export interface Channel {
  id: number; name: string; description: string; rules: ChannelRules; color: string; bitrate: number
  last_played_at: string | null; created_at: string; updated_at: string
  listen_url: string     // for other players and radio apps (the token is the secret)
  playlist_url: string   // .m3u pointing at listen_url
  on_air: boolean; now: string
}

export interface LibrarySong { id: string; title: string; artist: string; album: string; year: number; genre: string; cover: string }
export interface LibraryGenre { value: string; songCount: number }
export interface LibraryPlaylist { id: string; name: string; songCount: number; duration: number }
export interface LibraryInfo { ok: boolean; error?: string; not_connected?: boolean; via?: ConnVia; genres: LibraryGenre[]; playlists: LibraryPlaylist[] }
export interface ChannelPreview { ok: boolean; error?: string; not_connected?: boolean; songs: LibrarySong[]; count: number; estimate: boolean }
export interface ChannelNow {
  on_air: boolean; listeners: number; error?: string
  song?: { id: string; title: string; artist: string; album: string; year: number; duration: number; cover: string }
  started_at?: string; recent: Heard[]
}

// ---- listening ----

export interface Listen {
  id: number; station_id: number | null; channel_id: number | null; name: string
  client: 'web' | 'app' | 'import'; started_at: string; ended_at: string; seconds: number
}

export interface Heard {
  id: number; station_id: number | null; channel_id: number | null; source: string
  title: string; artist: string; song: string; song_id: string; saved: boolean; heard_at: string
}

/** One player session's stream as the server sees it (GET /now?sid=). */
export interface NowPlaying {
  playing: boolean; sid?: string; kind?: 'station' | 'channel'; id?: number; name?: string
  title?: string; artist?: string; song?: string; song_id?: string; title_since?: string; started_at?: string
}

export interface TopEntry { name: string; kind: 'station' | 'channel'; id: number; seconds: number; count: number }

export interface Dashboard {
  playing: NowPlaying[]
  favorites: Station[]; recent: Station[]; channels: Channel[]; heard: Heard[]
  today_seconds: number; week_seconds: number; week_top: TopEntry[]; failing: number
}

export interface History {
  from: string; to: string; total_seconds: number; sessions: number
  days: { date: string; seconds: number }[]; hours: number[]; top: TopEntry[]; listens: Listen[]
  heard_count: number; saved_count: number
}

export interface LibraryMatch { ok: boolean; error?: string; not_connected?: boolean; in_library?: boolean; song?: { id: string; title: string; artist: string; album: string }; open_url?: string; search_url?: string }
