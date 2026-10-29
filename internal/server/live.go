package server

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// ---- what is playing, and listening time ----
//
// Every stream the server plays out (a station through the relay, a channel
// from the library) is a liveStream, keyed by a session id the player
// makes up (?sid=…). The player asks /api/v1/now?sid=… for the song; the
// stream itself keeps the listening log: a listen row once it has played
// 30 seconds, moved on every half minute, closed when the player goes.
// HLS stations play straight in the browser, so their player sends a beat
// every 30 seconds instead (/api/v1/listens/beat).

const listenMinSeconds = 30

type liveStream struct {
	sid      string
	uid      int64
	kind     string // station | channel
	id       int64
	name     string
	client   string // web | app | beat
	started  time.Time
	lastSeen time.Time
	listenID int64

	title, artist, song string
	songID, album       string
	duration            int
	titleSince          time.Time
}

func (l *liveStream) view() map[string]any {
	out := map[string]any{"sid": l.sid, "kind": l.kind, "id": l.id, "name": l.name, "client": l.client,
		"started_at": l.started.UTC().Format(time.RFC3339), "title": l.title, "artist": l.artist, "song": l.song,
		"song_id": l.songID, "album": l.album, "duration": l.duration}
	if !l.titleSince.IsZero() {
		out["title_since"] = l.titleSince.UTC().Format(time.RFC3339)
	}
	if l.kind == "station" {
		out["station_id"] = l.id
	} else {
		out["channel_id"] = l.id
	}
	return out
}

func validSid(sid string) bool {
	if len(sid) < 6 || len(sid) > 64 {
		return false
	}
	for _, c := range sid {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// liveStart begins (or, for the same session, takes over) a stream.
func (s *Server) liveStart(ctx context.Context, uid int64, kind string, id int64, name, sid, client string) *liveStream {
	if !validSid(sid) {
		sid = store.RandomToken(9)
	}
	s.liveMu.Lock()
	old := s.live[sid]
	if old != nil && old.uid != uid {
		sid, old = store.RandomToken(9), nil // somebody else's id: never share
	}
	s.liveMu.Unlock()
	if old != nil {
		s.liveEnd(ctx, old)
	}
	now := time.Now()
	l := &liveStream{sid: sid, uid: uid, kind: kind, id: id, name: name, client: client, started: now, lastSeen: now}
	s.liveMu.Lock()
	s.live[sid] = l
	s.liveMu.Unlock()
	return l
}

// liveTouch keeps the listen row up to date; call it every few seconds.
func (s *Server) liveTouch(ctx context.Context, l *liveStream) {
	s.liveMu.Lock()
	l.lastSeen = time.Now()
	secs := int(l.lastSeen.Sub(l.started).Seconds())
	id := l.listenID
	s.liveMu.Unlock()
	if secs < listenMinSeconds {
		return
	}
	ended := l.lastSeen.UTC().Format(time.RFC3339Nano)
	if id > 0 {
		_ = s.store.ExtendListen(ctx, l.uid, id, ended, secs)
		return
	}
	row := &store.Listen{Name: l.name, Client: l.client, StartedAt: l.started.UTC().Format(time.RFC3339Nano), EndedAt: ended, Seconds: secs}
	if l.kind == "station" {
		row.StationID = &l.id
	} else {
		row.ChannelID = &l.id
	}
	if row.Client == "beat" {
		row.Client = "web"
	}
	if err := s.store.AddListen(ctx, l.uid, row); err == nil {
		s.liveMu.Lock()
		l.listenID = row.ID
		s.liveMu.Unlock()
	}
}

// liveEnd closes a stream: the final listening time, and an event for
// anything longer than a minute.
func (s *Server) liveEnd(ctx context.Context, l *liveStream) {
	s.liveMu.Lock()
	if s.live[l.sid] == l {
		delete(s.live, l.sid)
	}
	s.liveMu.Unlock()
	ctx = context.WithoutCancel(ctx)
	s.liveTouch(ctx, l)
	secs := int(l.lastSeen.Sub(l.started).Seconds())
	if secs >= 60 && l.listenID > 0 {
		s.publish(ctx, l.uid, "radio.listened", l.name, map[string]any{"kind": l.kind, "id": l.id, "name": l.name,
			"minutes": secs / 60, "started_at": l.started.UTC().Format(time.RFC3339), "listen_id": l.listenID})
	}
}

// liveTitle records the song a station or channel just started.
func (s *Server) liveTitle(ctx context.Context, l *liveStream, h store.Heard) {
	s.liveMu.Lock()
	same := l.title == h.Title
	if !same {
		l.title, l.artist, l.song, l.songID, l.titleSince = h.Title, h.Artist, h.Song, h.SongID, time.Now()
	}
	s.liveMu.Unlock()
	if same || h.Title == "" {
		return
	}
	if l.kind == "station" {
		h.StationID = &l.id
	} else {
		h.ChannelID = &l.id
	}
	h.Source = l.name
	_, _ = s.store.AddHeard(context.WithoutCancel(ctx), l.uid, &h)
}

// splitTitle turns "Artist - Song" into its parts (stations mostly send that).
func splitTitle(t string) (artist, song string) {
	t = strings.TrimSpace(t)
	for _, sep := range []string{" - ", " – ", " — ", " ~ "} {
		if a, b, ok := strings.Cut(t, sep); ok && strings.TrimSpace(a) != "" && strings.TrimSpace(b) != "" {
			return strings.TrimSpace(a), strings.TrimSpace(b)
		}
	}
	return "", t
}

// usefulTitle filters out what stations send between songs.
func usefulTitle(title, station string) bool {
	t := strings.TrimSpace(title)
	if len([]rune(t)) < 3 || strings.EqualFold(t, station) || strings.EqualFold(t, "unknown") || t == "-" {
		return false
	}
	lt := strings.ToLower(t)
	for _, junk := range []string{"advert", "commercial", "jingle", "station id", "spot block", "adbreak", "ad break", "www.", "http"} {
		if strings.Contains(lt, junk) {
			return false
		}
	}
	return true
}

// liveFor lists one person's streams playing now.
func (s *Server) liveFor(uid int64) []map[string]any {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	var list []*liveStream
	for _, l := range s.live {
		if l.uid == uid {
			list = append(list, l)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].started.After(list[j].started) })
	out := make([]map[string]any, 0, len(list))
	for _, l := range list {
		out = append(out, l.view())
	}
	return out
}

// reapBeats ends HLS listens whose player stopped sending beats.
func (s *Server) reapBeats(ctx context.Context) {
	s.liveMu.Lock()
	var gone []*liveStream
	for _, l := range s.live {
		if l.client == "beat" && time.Since(l.lastSeen) > 90*time.Second {
			gone = append(gone, l)
		}
	}
	s.liveMu.Unlock()
	for _, l := range gone {
		s.liveEnd(ctx, l)
	}
}

// handleNow is what plays now: one session (?sid) or all of the person's.
func (s *Server) handleNow(w http.ResponseWriter, r *http.Request) {
	uid := actorID(r.Context())
	sid := r.URL.Query().Get("sid")
	if sid == "" {
		writeJSON(w, http.StatusOK, map[string]any{"playing": s.liveFor(uid)})
		return
	}
	s.liveMu.Lock()
	l := s.live[sid]
	var v map[string]any
	if l != nil && l.uid == uid {
		v = l.view()
	}
	s.liveMu.Unlock()
	if v == nil {
		writeJSON(w, http.StatusOK, map[string]any{"playing": false})
		return
	}
	v["playing"] = true
	writeJSON(w, http.StatusOK, v)
}

// handleListenBeat keeps an HLS station's listen going (the browser plays
// those itself). {sid, station_id}; {stop: true} ends it.
func (s *Server) handleListenBeat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		Sid       string `json:"sid"`
		StationID int64  `json:"station_id"`
		Stop      bool   `json:"stop"`
	}
	if err := decode(r, &in); err != nil || !validSid(in.Sid) {
		writeErr(w, http.StatusBadRequest, "sid is required")
		return
	}
	s.liveMu.Lock()
	l := s.live[in.Sid]
	if l != nil && l.uid != uid {
		l = nil
	}
	s.liveMu.Unlock()
	if in.Stop {
		if l != nil {
			s.liveEnd(ctx, l)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if l == nil || l.kind != "station" || l.id != in.StationID {
		st, err := s.store.GetStation(ctx, uid, in.StationID)
		if err != nil {
			s.fail(w, err, "station")
			return
		}
		l = s.liveStart(ctx, uid, "station", st.ID, st.Name, in.Sid, "beat")
		_ = s.store.MarkStationPlayed(ctx, uid, st.ID)
		s.directoryClick(st.DirectoryID)
	}
	s.liveTouch(ctx, l)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sid": l.sid})
}
