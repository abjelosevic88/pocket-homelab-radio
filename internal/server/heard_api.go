package server

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-radio/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// ---- songs you heard ----

func (s *Server) handleListHeard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	f := store.HeardFilter{Saved: queryBool(r, "saved"), Query: q.Get("q"), StationID: int64(queryInt(r, "station_id")),
		ChannelID: int64(queryInt(r, "channel_id")), Before: q.Get("before"), Limit: queryInt(r, "limit")}
	list, err := s.store.ListHeard(ctx, actorID(ctx), f)
	if err != nil {
		s.fail(w, err, "heard")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleSaveHeard keeps a heard song (PUT {saved}); saving one also adds it
// to the person's Pocket Music playlist when it is in the library and
// Pocket Music is connected.
func (s *Server) handleSaveHeard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		Saved bool `json:"saved"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	id := idParam(r, "id")
	h, err := s.store.GetHeard(ctx, uid, id)
	if err != nil {
		s.fail(w, err, "song")
		return
	}
	if err := s.store.SetHeardSaved(ctx, uid, id, in.Saved); err != nil {
		s.fail(w, err, "song")
		return
	}
	h.Saved = in.Saved
	if in.Saved {
		s.publish(ctx, uid, "radio.song_saved", h.Title, map[string]any{"heard_id": h.ID, "title": h.Title, "artist": h.Artist, "song": h.Song,
			"source": h.Source, "heard_at": h.HeardAt})
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) handleDeleteHeard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.store.DeleteHeard(ctx, actorID(ctx), idParam(r, "id")); err != nil {
		s.fail(w, err, "song")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// songQuery is what to search the library for.
func songQuery(h *store.Heard) string {
	if h.Artist != "" && h.Song != "" {
		return h.Artist + " " + h.Song
	}
	return h.Title
}

// bestMatch picks the library song that is the heard one, or nil.
func bestMatch(h *store.Heard, songs []pocket.Song) *pocket.Song {
	norm := func(s string) string {
		s = strings.ToLower(s)
		if i := strings.IndexAny(s, "(["); i > 0 {
			s = s[:i]
		}
		return strings.Join(strings.Fields(s), " ")
	}
	want := norm(firstNonEmpty(h.Song, h.Title))
	for i, x := range songs {
		if norm(x.Title) == want && (h.Artist == "" || strings.Contains(norm(x.Artist), norm(h.Artist)) || strings.Contains(norm(h.Artist), norm(x.Artist))) {
			return &songs[i]
		}
	}
	return nil
}

// handleHeardLibrary says whether a heard song is in the person's music
// library (through Pocket Music): GET /api/v1/heard/{id}/library.
func (s *Server) handleHeardLibrary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	h, err := s.store.GetHeard(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "song")
		return
	}
	c := s.clients(ctx, uid)
	sib, err := c.conn(ctx, appMusic, scopePlaylists)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "not_connected": isNotConnected(err)})
		return
	}
	songs, err := sib.client.SearchSongs(ctx, songQuery(h), 10)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "Pocket Music: " + err.Error()})
		return
	}
	m := bestMatch(h, songs)
	out := map[string]any{"ok": true, "in_library": m != nil, "search_url": strings.TrimRight(sib.public, "/") + "/songs?q=" + url.QueryEscape(songQuery(h))}
	if m != nil {
		out["song"] = m
		if m.AlbumID != "" && sib.public != "" {
			out["open_url"] = strings.TrimRight(sib.public, "/") + "/albums/" + url.PathEscape(m.AlbumID)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleHeardToPlaylist adds a heard song from the library to the person's
// playlist in Pocket Music (Settings → "Saved songs go to"), making the
// playlist the first time. POST /api/v1/heard/{id}/playlist.
func (s *Server) handleHeardToPlaylist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	h, err := s.store.GetHeard(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "song")
		return
	}
	c := s.clients(ctx, uid)
	sib, err := c.conn(ctx, appMusic, scopePlaylists)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "not_connected": isNotConnected(err)})
		return
	}
	songID := h.SongID
	if songID == "" {
		songs, err := sib.client.SearchSongs(ctx, songQuery(h), 10)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "Pocket Music: " + err.Error()})
			return
		}
		m := bestMatch(h, songs)
		if m == nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "this song is not in your music library"})
			return
		}
		songID = m.ID
	}
	name := firstNonEmpty(c.st.MusicPlaylist, "Heard on the radio")
	pls, err := sib.client.Playlists(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "Pocket Music: " + err.Error()})
		return
	}
	plID := ""
	for _, p := range pls {
		if strings.EqualFold(p.Name, name) {
			plID = p.ID
			break
		}
	}
	if plID == "" {
		p, err := sib.client.CreatePlaylist(ctx, name, "Songs saved in Pocket Radio", []string{songID})
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "Pocket Music: " + err.Error()})
			return
		}
		plID = p.ID
	} else if err := sib.client.AddToPlaylist(ctx, plID, []string{songID}); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "Pocket Music: " + err.Error()})
		return
	}
	link := ""
	if sib.public != "" && plID != "" {
		link = strings.TrimRight(sib.public, "/") + "/playlists/" + url.PathEscape(plID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "playlist": name, "url": link})
}
