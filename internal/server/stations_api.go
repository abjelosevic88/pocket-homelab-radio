package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// ---- stations ----

type stationInput struct {
	Name        *string `json:"name"`
	URL         *string `json:"url"`
	URLResolved *string `json:"url_resolved"`
	Homepage    *string `json:"homepage"`
	LogoURL     *string `json:"logo_url"`
	Tags        *string `json:"tags"`
	CountryCode *string `json:"country_code"`
	Language    *string `json:"language"`
	Codec       *string `json:"codec"`
	Bitrate     *int    `json:"bitrate"`
	HLS         *bool   `json:"hls"`
	DirectoryID *string `json:"directory_id"`
	Favorite    *bool   `json:"favorite"`
	Notes       *string `json:"notes"`
	Force       bool    `json:"force"` // keep it even if it does not play now
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// apply copies the sent fields; "" = fine, else what is wrong.
func (in stationInput) apply(st *store.Station) string {
	set := func(p *string, dst *string, n int) {
		if p != nil {
			*dst = clip(*p, n)
		}
	}
	set(in.Name, &st.Name, 120)
	set(in.URL, &st.URL, 2000)
	set(in.URLResolved, &st.URLResolved, 2000)
	set(in.Homepage, &st.Homepage, 500)
	set(in.LogoURL, &st.LogoURL, 1000)
	set(in.Language, &st.Language, 80)
	set(in.Codec, &st.Codec, 20)
	set(in.DirectoryID, &st.DirectoryID, 64)
	set(in.Notes, &st.Notes, 2000)
	if in.Tags != nil {
		st.Tags = clip(store.JoinTags(*in.Tags), 300)
	}
	if in.CountryCode != nil {
		st.CountryCode = strings.ToUpper(clip(*in.CountryCode, 2))
	}
	if in.Bitrate != nil && *in.Bitrate >= 0 && *in.Bitrate < 10000 {
		st.Bitrate = *in.Bitrate
	}
	if in.HLS != nil {
		st.HLS = *in.HLS
	}
	if in.Favorite != nil {
		st.Favorite = *in.Favorite
	}
	if !validStreamURL(st.URL) {
		return "the stream address must start with http:// or https://"
	}
	if st.URLResolved != "" && !validStreamURL(st.URLResolved) {
		st.URLResolved = ""
	}
	if st.LogoURL != "" && !validStreamURL(st.LogoURL) {
		st.LogoURL = ""
	}
	if st.Homepage != "" && !validStreamURL(st.Homepage) {
		st.Homepage = ""
	}
	return ""
}

func (s *Server) handleListStations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f := store.StationFilter{Query: r.URL.Query().Get("q"), Limit: queryInt(r, "limit")}
	switch r.URL.Query().Get("view") {
	case "recent":
		f.Recent = true
	case "all":
	default:
		f.Favorites = true
	}
	list, err := s.store.ListStations(ctx, actorID(ctx), f)
	if err != nil {
		s.fail(w, err, "stations")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetStation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.store.GetStation(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "station")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// fillFromProbe opens the address and fills in what the stream says.
func (s *Server) fillFromProbe(ctx context.Context, st *store.Station) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	p, err := s.probeStream(ctx, st.URL)
	if err != nil {
		return err
	}
	st.URLResolved, st.HLS = p.URL, p.HLS
	if st.URLResolved == st.URL {
		st.URLResolved = ""
	}
	if p.Codec != "" {
		st.Codec = p.Codec
	}
	if p.Bitrate > 0 {
		st.Bitrate = p.Bitrate
	}
	if st.Name == "" {
		st.Name = clip(p.Name, 120)
	}
	if st.Tags == "" && p.Genre != "" {
		st.Tags = clip(store.JoinTags(strings.ReplaceAll(p.Genre, "/", ",")), 300)
	}
	if st.Homepage == "" && validStreamURL(p.Homepage) {
		st.Homepage = p.Homepage
	}
	st.Health = "ok"
	return nil
}

// handleCreateStation adds a station (or, when the person already has that
// address, makes it a favourite again). From the directory nothing is
// probed — the directory already checked it; a typed address is opened
// first so a typo is caught at once. favorite=false keeps a station only
// for "recently played" (playing a search result).
func (s *Server) handleCreateStation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in stationInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	st := store.Station{Favorite: true, Source: "manual"}
	if msg := in.apply(&st); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if app := linkApp(ctx); app != "" {
		st.Source = app
		st.Favorite = true
	} else if st.DirectoryID != "" {
		st.Source = "directory"
	}
	have, err := s.store.StationByDirectoryID(ctx, uid, st.DirectoryID)
	if err != nil {
		have, err = s.store.StationByURL(ctx, uid, st.URL)
	}
	if err == nil {
		if st.Favorite && !have.Favorite {
			have.Favorite = true
			_ = s.store.UpdateStation(ctx, uid, have)
			s.publish(ctx, uid, "radio.station_added", have.Name, map[string]any{"station_id": have.ID, "name": have.Name, "url": have.URL})
		}
		writeJSON(w, http.StatusOK, have)
		return
	}
	if st.Source != "directory" && !s.cfg.DemoData {
		if err := s.fillFromProbe(ctx, &st); err != nil && !in.Force {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "It does not play: " + err.Error(), "probe_failed": true})
			return
		}
	}
	if st.Name == "" {
		st.Name = hostOf(st.URL)
	}
	if err := s.store.CreateStation(ctx, uid, &st); err != nil {
		s.fail(w, err, "add station")
		return
	}
	if st.Favorite {
		s.publish(ctx, uid, "radio.station_added", st.Name, map[string]any{"station_id": st.ID, "name": st.Name, "url": st.URL})
	}
	writeJSON(w, http.StatusCreated, st)
}

func hostOf(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	h, _, _ := strings.Cut(u, "/")
	h, _, _ = strings.Cut(h, ":")
	return h
}

func (s *Server) handleUpdateStation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	st, err := s.store.GetStation(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "station")
		return
	}
	var in stationInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	oldURL := st.URL
	if msg := in.apply(st); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if st.Name == "" {
		writeErr(w, http.StatusBadRequest, "the station needs a name")
		return
	}
	if st.URL != oldURL {
		st.URLResolved, st.HLS, st.DirectoryID = "", false, ""
		if err := s.fillFromProbe(ctx, st); err != nil && !in.Force && !s.cfg.DemoData {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "It does not play: " + err.Error(), "probe_failed": true})
			return
		}
	}
	if err := s.store.UpdateStation(ctx, uid, st); err != nil {
		s.fail(w, err, "save station")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleDeleteStation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.store.DeleteStation(ctx, actorID(ctx), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete station")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReorderStations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.store.ReorderStations(ctx, actorID(ctx), in.IDs); err != nil {
		s.fail(w, err, "reorder")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleCheckStation tries a station now.
func (s *Server) handleCheckStation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	st, err := s.store.GetStation(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "station")
		return
	}
	ok, note := s.checkStation(ctx, uid, st)
	st, err = s.store.SetStationHealth(ctx, uid, st.ID, ok, note, failingAfter)
	if err != nil {
		s.fail(w, err, "station")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "note": note, "station": st})
}

// ---- import and export ----

func (s *Server) handleExportM3U(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := s.store.ListStations(ctx, actorID(ctx), store.StationFilter{Favorites: true})
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	setAttachment(w, "pocket-radio-stations.m3u", "audio/x-mpegurl")
	exportM3U(w, list)
}

// handleImportPlaylist adds the stations of a .m3u / .pls file (the body,
// or JSON {"text": …}). Addresses the person already has are skipped, so a
// second import adds nothing.
func (s *Server) handleImportPlaylist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	b, err := s.readBody(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "could not read the file")
		return
	}
	if strings.HasPrefix(strings.TrimSpace(string(b)), "{") {
		var in struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(b, &in) == nil {
			b = []byte(in.Text)
		}
	}
	if isHLS(b) {
		writeErr(w, http.StatusBadRequest, "this is the stream of one HLS station, not a list of stations: add it by its address instead")
		return
	}
	entries := parsePlaylist(b)
	if len(entries) == 0 {
		writeErr(w, http.StatusBadRequest, "no stations in this file (expected .m3u or .pls)")
		return
	}
	added, skipped := s.addEntries(ctx, uid, entries, "import")
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "skipped": skipped})
}

func (s *Server) addEntries(ctx context.Context, uid int64, entries []playlistEntry, source string) (added, skipped int) {
	for _, e := range entries {
		if _, err := s.store.StationByURL(ctx, uid, e.URL); err == nil {
			skipped++
			continue
		}
		st := store.Station{Name: clip(firstNonEmpty(e.Name, hostOf(e.URL)), 120), URL: e.URL, Tags: clip(store.JoinTags(e.Tags), 300),
			Favorite: true, Source: source}
		if validStreamURL(e.Logo) {
			st.LogoURL = e.Logo
		}
		if strings.HasSuffix(strings.ToLower(strings.SplitN(e.URL, "?", 2)[0]), ".m3u8") {
			st.HLS = true
		}
		if err := s.store.CreateStation(ctx, uid, &st); err != nil {
			skipped++
			continue
		}
		added++
	}
	return added, skipped
}

// handleImportNavidrome copies the internet radio stations kept in Navidrome.
func (s *Server) handleImportNavidrome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	nav, err := s.navidromeFor(ctx, uid)
	if err != nil {
		s.libraryError(w, err)
		return
	}
	list, err := nav.internetRadios(ctx)
	if err != nil {
		s.libraryError(w, err)
		return
	}
	var entries []playlistEntry
	for _, x := range list {
		if validStreamURL(x.Stream) {
			entries = append(entries, playlistEntry{Name: x.Name, URL: x.Stream})
		}
	}
	added, skipped := s.addEntries(ctx, uid, entries, "navidrome")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "added": added, "skipped": skipped, "found": len(list)})
}
