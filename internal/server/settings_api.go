package server

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-radio/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-radio/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-radio/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// ---- settings ----

func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:4] + "••••" + s[len(s)-4:]
}

// publicSettings is what one person sees: their settings with the secrets
// masked and, per Pocket app, the mode in effect (<app>_mode), whether it
// works and how (<app>_via: core | direct | "") and why not (<app>_detail).
func (s *Server) publicSettings(ctx context.Context, u *store.User, st store.Settings) map[string]any {
	out := map[string]any{
		"core_configured": s.core.Configured(), "core_registered": s.coreStatus()["registered"],
		"title": st.Title, "date_format": st.DateFormat, "time_format": st.TimeFormat, "week_start": st.WeekStart,
		"country": st.Country, "sleep_minutes": st.SleepMinutes, "channel_bitrate": st.ChannelBitrate, "keep_heard_days": st.KeepHeardDays,
		"notify_broken": st.NotifyBroken, "notify_url": st.NotifyURL, "notify_format": st.NotifyFormat, "notify_configured": st.NotifyURL != "",
		"navidrome_url": st.NavidromeURL, "navidrome_user": st.NavidromeUser, "navidrome_password": mask(st.NavidromePassword),
		"navidrome_mode": s.connMode(st.NavidromeMode, st.NavidromePassword),
		"music_url":      st.MusicURL, "music_token": mask(st.MusicToken), "music_public_url": st.MusicPublicURL, "music_playlist": st.MusicPlaylist,
		"ai_provider": st.AIProvider, "ai_pocket_model": st.AIPocketModel, "openai_api_key": mask(st.OpenAIKey), "openai_model": st.OpenAIModel, "openai_base_url": st.OpenAIBaseURL,
		"setup_complete": st.SetupComplete, "base_url": s.cfg.BaseURL, "directory": s.directoryOn(), "local_streams": s.cfg.LocalStreams,
	}
	for _, a := range pocketApps {
		mode, via, detail := s.connStatus(ctx, u, st, a)
		out[a.slug+"_mode"], out[a.slug+"_via"], out[a.slug+"_detail"] = mode, via, detail
		out[a.slug+"_configured"] = via != ""
		_, _, _, public := connSettings(st, a)
		if public == "" && via != "" {
			public = s.resolveSibling(ctx, a.slug)
		}
		out[a.slug+"_open_url"] = public
	}
	return out
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), userFrom(r.Context()), st))
}

var countryCode = regexp.MustCompile(`^[A-Z]{2}$`)

// handlePutSettings saves the caller's personal settings. The box-wide title
// is only taken from an admin.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r.Context())
	isAdmin := me.Role == "admin"
	cur, err := s.store.GetSettings(r.Context(), me.ID)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	var in store.Settings
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	bad := func(msg string) { writeErr(w, http.StatusBadRequest, msg) }
	if keys["title"] && isAdmin {
		cur.Title = strings.TrimSpace(in.Title)
		if cur.Title == "" {
			cur.Title = store.DefaultTitle
		}
	}
	if keys["date_format"] {
		cur.DateFormat = in.DateFormat
	}
	if keys["time_format"] {
		cur.TimeFormat = in.TimeFormat
	}
	if keys["week_start"] && (in.WeekStart == 1 || in.WeekStart == 7 || in.WeekStart == 6) {
		cur.WeekStart = in.WeekStart
	}
	secret := func(k string, dst *string, v string) {
		if keys[k] && !strings.Contains(v, "••") {
			*dst = strings.TrimSpace(v)
		}
	}
	url := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimRight(strings.TrimSpace(v), "/")
		}
	}
	text := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimSpace(v)
		}
	}
	boolIn := func(k string, dst *bool, v bool) {
		if keys[k] {
			*dst = v
		}
	}
	if keys["country"] {
		c := strings.ToUpper(strings.TrimSpace(in.Country))
		if c != "" && !countryCode.MatchString(c) {
			bad("the country is a two-letter code, like BA or DE")
			return
		}
		cur.Country = c
	}
	if keys["sleep_minutes"] {
		if in.SleepMinutes < 5 || in.SleepMinutes > 240 {
			bad("the sleep timer is between 5 and 240 minutes")
			return
		}
		cur.SleepMinutes = in.SleepMinutes
	}
	if keys["channel_bitrate"] {
		if !bitrates[in.ChannelBitrate] {
			bad("the sound quality is 96, 128, 160, 192, 256 or 320 kbit/s")
			return
		}
		cur.ChannelBitrate = in.ChannelBitrate
	}
	if keys["keep_heard_days"] {
		if in.KeepHeardDays < 0 || in.KeepHeardDays > 3650 {
			bad("keep heard songs for 0 (always) to 3650 days")
			return
		}
		cur.KeepHeardDays = in.KeepHeardDays
	}
	boolIn("notify_broken", &cur.NotifyBroken, in.NotifyBroken)
	text("notify_url", &cur.NotifyURL, in.NotifyURL)
	if keys["notify_format"] && in.NotifyFormat != "" {
		cur.NotifyFormat = in.NotifyFormat
	}
	url("navidrome_url", &cur.NavidromeURL, in.NavidromeURL)
	text("navidrome_user", &cur.NavidromeUser, in.NavidromeUser)
	secret("navidrome_password", &cur.NavidromePassword, in.NavidromePassword)
	url("music_url", &cur.MusicURL, in.MusicURL)
	secret("music_token", &cur.MusicToken, in.MusicToken)
	url("music_public_url", &cur.MusicPublicURL, in.MusicPublicURL)
	if keys["music_playlist"] {
		cur.MusicPlaylist = clip(in.MusicPlaylist, 80)
		if cur.MusicPlaylist == "" {
			cur.MusicPlaylist = "Heard on the radio"
		}
	}
	if keys["ai_provider"] {
		if !validAIProvider(in.AIProvider) {
			bad("AI is automatic, Pocket AI or OpenAI")
			return
		}
		cur.AIProvider = in.AIProvider
	}
	if keys["ai_pocket_model"] {
		if !validPocketModel(in.AIPocketModel) {
			bad("unknown model")
			return
		}
		cur.AIPocketModel = in.AIPocketModel
	}
	secret("openai_api_key", &cur.OpenAIKey, in.OpenAIKey)
	text("openai_model", &cur.OpenAIModel, in.OpenAIModel)
	url("openai_base_url", &cur.OpenAIBaseURL, in.OpenAIBaseURL)
	mode := func(k string, dst *string, v string) {
		if keys[k] && (v == modeCore || v == modeDirect || v == modeOff) {
			*dst = v
		}
	}
	mode("music_mode", &cur.MusicMode, in.MusicMode)
	mode("navidrome_mode", &cur.NavidromeMode, in.NavidromeMode)
	if err := s.store.SaveUserSettings(r.Context(), me.ID, cur); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	if isAdmin && keys["title"] {
		if err := s.store.SaveBoxSettings(r.Context(), cur); err != nil {
			s.fail(w, err, "save settings")
			return
		}
	}
	for _, k := range modeKeys {
		if keys[k] {
			s.reportModesSoon()
			break
		}
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), me, cur))
}

// handleIntegrationTest checks a connection with the caller's own saved
// settings (or the URL/key typed into the form).
func (s *Server) handleIntegrationTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Which  string `json:"which"`
		URL    string `json:"url"`
		Key    string `json:"key"`
		User   string `json:"user"`
		Format string `json:"format"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	uid := actorID(r.Context())
	st, _ := s.store.GetSettings(r.Context(), uid)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	keyOr := func(saved string) string {
		if strings.Contains(in.Key, "••") || in.Key == "" {
			return saved
		}
		return in.Key
	}
	urlOr := func(saved string) string {
		if strings.TrimSpace(in.URL) == "" {
			return saved
		}
		return strings.TrimRight(strings.TrimSpace(in.URL), "/")
	}
	var msg string
	var err error
	if a, ok := appBySlug(in.Which); ok {
		mode, savedURL, savedTok, _ := connSettings(st, a)
		typed := in.Key != "" && !strings.Contains(in.Key, "••")
		var conn *siblingConn
		if tok := keyOr(savedTok); tok != "" && (typed || s.connMode(mode, savedTok) == modeDirect) {
			base := urlOr(savedURL)
			if base == "" {
				base = s.resolveSibling(ctx, a.slug)
			}
			conn = &siblingConn{client: pocket.New(base, tok, 15*time.Second), via: modeDirect}
		} else {
			conn, err = s.clients(ctx, uid).conn(ctx, a, a.scopes...)
		}
		if err == nil {
			how := "direct connection"
			if conn.via == modeCore {
				how = "through Pocket Core"
			}
			var v string
			if v, err = conn.client.Health(ctx); err == nil {
				var pls []pocket.Playlist
				pls, err = conn.client.Playlists(ctx)
				msg = fmt.Sprintf("Pocket Music %s, %s, %d playlists", v, how, len(pls))
			}
		}
	} else if in.Which == "navidrome" {
		var n *navidrome
		if in.User != "" || (in.Key != "" && !strings.Contains(in.Key, "••")) || in.URL != "" {
			n = &navidrome{base: urlOr(st.NavidromeURL), user: firstNonEmpty(in.User, st.NavidromeUser), pass: keyOr(st.NavidromePassword), via: modeDirect, http: &http.Client{Timeout: 15 * time.Second}}
		} else {
			n, err = s.navidromeFor(ctx, uid)
		}
		if err == nil {
			var gs []navGenre
			if gs, err = n.genres(ctx); err == nil {
				how := "direct connection"
				if n.via == modeCore {
					how = "through Pocket Core"
				}
				songs := 0
				for _, g := range gs {
					songs += g.SongCount
				}
				msg = fmt.Sprintf("Navidrome, %s, %d genres", how, len(gs))
				if songs > 0 {
					msg = fmt.Sprintf("Navidrome, %s, %d genres, %d songs", how, len(gs), songs)
				}
			}
		}
	} else if in.Which == "openai" {
		c := ai.New(urlOr(st.OpenAIBaseURL), keyOr(st.OpenAIKey), st.OpenAIModel, 20*time.Second)
		var ids []string
		if ids, err = c.Models(ctx); err == nil {
			msg = fmt.Sprintf("The key works (%d models)", len(ids))
		}
	} else if in.Which == "notify" {
		f := in.Format
		if f == "" {
			f = st.NotifyFormat
		}
		n := notify.New(urlOr(st.NotifyURL), f)
		err = n.Send(ctx, notify.Message{Title: "Pocket Radio", Body: "Notifications work. If a favourite station stops working, you will hear it here.", Priority: "info", Tags: []string{"radio"}})
		msg = "Test message sent via " + n.Format
	} else {
		writeErr(w, http.StatusBadRequest, "unknown integration")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if err := n.Send(ctx, notify.Message{Title: "Pocket Radio", Body: "Notifications work.", Priority: "info", Tags: []string{"radio"}}); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- system / backup / export ----

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	var size int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		size = fi.Size()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"core":      s.coreStatus(),
		"auth_mode": s.cfg.AuthMode, "data_dir": s.cfg.DataDir, "db_path": s.cfg.DBPath, "db_bytes": size,
		"uptime": time.Since(s.started).Round(time.Second).String(), "timezone": time.Now().Location().String(),
		"counts": s.store.CountsFor(r.Context(), actorID(r.Context())), "saved": s.saved.Load(),
	})
}

// handleBackup downloads the whole database, an admin-only box operation.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "backups")
	name := fmt.Sprintf("pocket-radio-%s.db", time.Now().Format("20060102-150405"))
	dst := filepath.Join(dir, name)
	if err := s.store.Backup(r.Context(), dst); err != nil {
		s.fail(w, err, "backup")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, dst)
}

// exportDoc is the JSON export of one person's data.
type exportDoc struct {
	App      string          `json:"app"`
	Version  string          `json:"version"`
	Exported string          `json:"exported_at"`
	Stations []store.Station `json:"stations"`
	Channels []store.Channel `json:"channels"`
	Listens  []store.Listen  `json:"listens"`
	Heard    []store.Heard   `json:"heard"`
}

func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	doc := exportDoc{App: "pocket-radio", Version: Version, Exported: store.Now()}
	var err error
	if doc.Stations, err = s.store.ListStations(ctx, uid, store.StationFilter{}); err != nil {
		s.fail(w, err, "export")
		return
	}
	doc.Channels, _ = s.store.ListChannels(ctx, uid)
	doc.Listens, _ = s.store.ListListens(ctx, uid, "", "", 0)
	doc.Heard, _ = s.store.ListHeard(ctx, uid, store.HeardFilter{Limit: 500})
	if saved, _ := s.store.ListHeard(ctx, uid, store.HeardFilter{Saved: true, Limit: 500}); len(saved) > 0 {
		seen := map[int64]bool{}
		for _, h := range doc.Heard {
			seen[h.ID] = true
		}
		for _, h := range saved {
			if !seen[h.ID] {
				doc.Heard = append(doc.Heard, h)
			}
		}
	}
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-radio-export.json"`)
	writeJSON(w, http.StatusOK, doc)
}

// handleExportCSV writes the listening log as CSV.
func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ls, err := s.store.ListListens(ctx, actorID(ctx), "", "", 0)
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-radio-listening.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"started", "ended", "minutes", "station", "kind", "player"})
	for _, l := range ls {
		kind := "station"
		if l.ChannelID != nil {
			kind = "channel"
		}
		_ = cw.Write([]string{l.StartedAt, l.EndedAt, strconv.Itoa(l.Seconds / 60), l.Name, kind, l.Client})
	}
	cw.Flush()
}

// handleImportJSON merges an export into the caller's own data. Stations
// are matched by address, channels by name, listens by start and name and
// heard songs by time and title, so importing the same file twice adds
// nothing.
func (s *Server) handleImportJSON(w http.ResponseWriter, r *http.Request) {
	var doc exportDoc
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, s.cfg.MaxBodyBytes))
	if err := dec.Decode(&doc); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if doc.App != "" && doc.App != "pocket-radio" {
		writeErr(w, http.StatusBadRequest, "this is not a Pocket Radio export")
		return
	}
	ctx := r.Context()
	uid := actorID(ctx)
	counts := map[string]int{"stations": 0, "channels": 0, "listens": 0, "heard": 0, "skipped": 0}
	stationIDs := map[int64]int64{}
	for _, st := range doc.Stations {
		old := st.ID
		if have, err := s.store.StationByURL(ctx, uid, st.URL); err == nil {
			stationIDs[old] = have.ID
			continue
		}
		n := store.Station{Favorite: st.Favorite, Source: "import", PlayCount: st.PlayCount, LastPlayedAt: st.LastPlayedAt}
		in := stationInput{Name: &st.Name, URL: &st.URL, URLResolved: &st.URLResolved, Homepage: &st.Homepage, LogoURL: &st.LogoURL, Tags: &st.Tags,
			CountryCode: &st.CountryCode, Language: &st.Language, Codec: &st.Codec, Bitrate: &st.Bitrate, HLS: &st.HLS, DirectoryID: &st.DirectoryID, Notes: &st.Notes}
		if msg := in.apply(&n); msg != "" || n.Name == "" {
			counts["skipped"]++
			continue
		}
		if err := s.store.CreateStation(ctx, uid, &n); err != nil {
			counts["skipped"]++
			continue
		}
		stationIDs[old] = n.ID
		counts["stations"]++
	}
	channelIDs := map[int64]int64{}
	have, _ := s.store.ListChannels(ctx, uid)
	byName := map[string]int64{}
	for _, c := range have {
		byName[strings.ToLower(c.Name)] = c.ID
	}
	for _, c := range doc.Channels {
		if id, ok := byName[strings.ToLower(c.Name)]; ok {
			channelIDs[c.ID] = id
			continue
		}
		n := store.Channel{Bitrate: 192}
		rules := c.Rules
		in := channelInput{Name: &c.Name, Description: &c.Description, Rules: &rules, Color: &c.Color, Bitrate: &c.Bitrate}
		if msg := in.apply(&n); msg != "" {
			counts["skipped"]++
			continue
		}
		if err := s.store.CreateChannel(ctx, uid, &n); err != nil {
			counts["skipped"]++
			continue
		}
		channelIDs[c.ID], byName[strings.ToLower(c.Name)] = n.ID, n.ID
		counts["channels"]++
	}
	mapID := func(m map[int64]int64, id *int64) *int64 {
		if id == nil {
			return nil
		}
		if n, ok := m[*id]; ok {
			return &n
		}
		return nil
	}
	known, _ := s.store.ListListens(ctx, uid, "", "", 0)
	seenL := map[string]bool{}
	for _, l := range known {
		seenL[l.StartedAt+"|"+l.Name] = true
	}
	for _, l := range doc.Listens {
		if seenL[l.StartedAt+"|"+l.Name] || l.StartedAt == "" {
			continue
		}
		l.StationID, l.ChannelID = mapID(stationIDs, l.StationID), mapID(channelIDs, l.ChannelID)
		if l.Client == "" {
			l.Client = "import"
		}
		if err := s.store.AddListen(ctx, uid, &l); err != nil {
			counts["skipped"]++
			continue
		}
		seenL[l.StartedAt+"|"+l.Name] = true
		counts["listens"]++
	}
	for _, h := range doc.Heard {
		if strings.TrimSpace(h.Title) == "" || h.HeardAt == "" {
			continue
		}
		h.StationID, h.ChannelID = mapID(stationIDs, h.StationID), mapID(channelIDs, h.ChannelID)
		h.ID = 0
		added, err := s.store.AddHeard(ctx, uid, &h)
		if err != nil {
			counts["skipped"]++
			continue
		}
		if added {
			counts["heard"]++
		} else if h.Saved {
			_ = s.store.SetHeardSaved(ctx, uid, h.ID, true)
		}
	}
	writeJSON(w, http.StatusOK, counts)
}
