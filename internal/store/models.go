package store

import "strings"

// User is an account on this box.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Station is an internet radio station a person keeps.
type Station struct {
	ID           int64   `json:"id"`
	UserID       int64   `json:"-"`
	Name         string  `json:"name"`
	URL          string  `json:"url"`
	URLResolved  string  `json:"url_resolved"`
	Homepage     string  `json:"homepage"`
	LogoURL      string  `json:"logo_url"`
	Tags         string  `json:"tags"`
	CountryCode  string  `json:"country_code"`
	Language     string  `json:"language"`
	Codec        string  `json:"codec"`
	Bitrate      int     `json:"bitrate"`
	HLS          bool    `json:"hls"`
	DirectoryID  string  `json:"directory_id"`
	Favorite     bool    `json:"favorite"`
	Position     int     `json:"position"`
	Notes        string  `json:"notes"`
	Source       string  `json:"source"`
	PlayCount    int     `json:"play_count"`
	LastPlayedAt *string `json:"last_played_at"`
	Health       string  `json:"health"`
	FailCount    int     `json:"fail_count"`
	CheckedAt    *string `json:"checked_at"`
	HealthNote   string  `json:"health_note"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

// StreamURL is the address actually played.
func (st Station) StreamURL() string {
	if st.URLResolved != "" {
		return st.URLResolved
	}
	return st.URL
}

// ChannelRules choose the songs of a channel from the music library. Every
// list is optional; songs must match all the rules that are set.
type ChannelRules struct {
	Genres         []string `json:"genres"`          // any of these (empty = any genre)
	ExcludeGenres  []string `json:"exclude_genres"`  // never these
	Artists        []string `json:"artists"`         // only these artists (empty = anyone)
	ExcludeArtists []string `json:"exclude_artists"` // never these
	Playlists      []string `json:"playlists"`       // Navidrome playlist ids: songs from these
	YearFrom       int      `json:"year_from"`
	YearTo         int      `json:"year_to"`
	StarredOnly    bool     `json:"starred_only"` // only songs starred (♥) in the library
	AvoidHours     int      `json:"avoid_hours"`  // no song again within this many hours
	ArtistGap      int      `json:"artist_gap"`   // songs between two by the same artist
}

// Channel is a station of a person's own, from their music library.
type Channel struct {
	ID           int64        `json:"id"`
	UserID       int64        `json:"-"`
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	Rules        ChannelRules `json:"rules"`
	Color        string       `json:"color"`
	Bitrate      int          `json:"bitrate"`
	ListenToken  string       `json:"-"`
	LastPlayedAt *string      `json:"last_played_at"`
	CreatedAt    string       `json:"created_at"`
	UpdatedAt    string       `json:"updated_at"`
}

// Listen is one stretch of listening.
type Listen struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"-"`
	StationID *int64 `json:"station_id"`
	ChannelID *int64 `json:"channel_id"`
	Name      string `json:"name"`
	Client    string `json:"client"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at"`
	Seconds   int    `json:"seconds"`
}

// Heard is a song heard on a station or a channel.
type Heard struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"-"`
	StationID *int64 `json:"station_id"`
	ChannelID *int64 `json:"channel_id"`
	Source    string `json:"source"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Song      string `json:"song"`
	SongID    string `json:"song_id"`
	Saved     bool   `json:"saved"`
	HeardAt   string `json:"heard_at"`
}

// Settings are the box-wide values plus one person's preferences and their
// own connections. Connection modes: "core" (an app link through Pocket
// Core), "direct" (URL + token) or "off"; "" = not chosen yet.
type Settings struct {
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`

	DateFormat string `json:"date_format"`
	TimeFormat string `json:"time_format"`
	WeekStart  int    `json:"week_start"` // 1 = Monday, 7 = Sunday

	// Listening.
	Country        string `json:"country"`         // ISO code for "Popular where you live"
	SleepMinutes   int    `json:"sleep_minutes"`   // the sleep timer's default
	ChannelBitrate int    `json:"channel_bitrate"` // new channels stream at this (kbit/s)
	KeepHeardDays  int    `json:"keep_heard_days"` // unsaved heard songs are forgotten after this (0 = keep)

	// Notifications.
	NotifyBroken bool   `json:"notify_broken"` // a favourite station stopped working
	NotifyURL    string `json:"notify_url"`
	NotifyFormat string `json:"notify_format"` // apprise | ntfy | json

	// Your music library for channels: Navidrome (Subsonic API) through a
	// Pocket Core login or a direct URL, user and password.
	NavidromeMode     string `json:"navidrome_mode"`
	NavidromeURL      string `json:"navidrome_url"`
	NavidromeUser     string `json:"navidrome_user"`
	NavidromePassword string `json:"navidrome_password"`

	// Pocket Music: is a heard song in your library, add it to a playlist.
	MusicMode      string `json:"music_mode"`
	MusicURL       string `json:"music_url"`
	MusicToken     string `json:"music_token"`
	MusicPublicURL string `json:"music_public_url"`
	MusicPlaylist  string `json:"music_playlist"` // playlist saved songs go to

	// AI for describing a channel in your own words.
	AIProvider    string `json:"ai_provider"` // auto | pocket | openai
	AIPocketModel string `json:"ai_pocket_model"`
	OpenAIKey     string `json:"openai_api_key"`
	OpenAIModel   string `json:"openai_model"`
	OpenAIBaseURL string `json:"openai_base_url"`
}

// SplitTags turns "a, b ,c" into clean lowercase tags.
func SplitTags(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.Split(s, ",") {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// JoinTags is the stored form of tags.
func JoinTags(s string) string { return strings.Join(SplitTags(s), ", ") }
