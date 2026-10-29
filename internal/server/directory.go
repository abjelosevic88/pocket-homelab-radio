package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// ---- the station directory ----
//
// radio-browser.info is the open, community-kept list of internet radio
// stations (about 50,000, free, no key). Searches are cached for ten
// minutes; the lists of genres and countries for a day.

type dirStation struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	URLResolved string `json:"url_resolved"`
	Homepage    string `json:"homepage"`
	Logo        string `json:"logo_url"`
	Tags        string `json:"tags"`
	CountryCode string `json:"country_code"`
	Country     string `json:"country"`
	Language    string `json:"language"`
	Codec       string `json:"codec"`
	Bitrate     int    `json:"bitrate"`
	HLS         bool   `json:"hls"`
	Votes       int    `json:"votes"`
	Clicks      int    `json:"clicks"`
	Working     bool   `json:"working"`
	SavedID     int64  `json:"saved_id,omitempty"`
	Favorite    bool   `json:"favorite,omitempty"`
}

type rbStation struct {
	UUID        string `json:"stationuuid"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	URLResolved string `json:"url_resolved"`
	Homepage    string `json:"homepage"`
	Favicon     string `json:"favicon"`
	Tags        string `json:"tags"`
	CountryCode string `json:"countrycode"`
	Country     string `json:"country"`
	Language    string `json:"language"`
	Codec       string `json:"codec"`
	Bitrate     int    `json:"bitrate"`
	HLS         int    `json:"hls"`
	Votes       int    `json:"votes"`
	ClickCount  int    `json:"clickcount"`
	LastCheckOK int    `json:"lastcheckok"`
}

func (r rbStation) view() dirStation {
	tags := strings.Split(r.Tags, ",")
	if len(tags) > 6 {
		tags = tags[:6]
	}
	codec := strings.ToUpper(r.Codec)
	if codec == "UNKNOWN" {
		codec = ""
	}
	return dirStation{ID: r.UUID, Name: strings.TrimSpace(r.Name), URL: r.URL, URLResolved: r.URLResolved, Homepage: r.Homepage, Logo: r.Favicon,
		Tags: strings.Join(tags, ", "), CountryCode: r.CountryCode, Country: r.Country, Language: r.Language, Codec: codec, Bitrate: r.Bitrate,
		HLS: r.HLS == 1, Votes: r.Votes, Clicks: r.ClickCount, Working: r.LastCheckOK == 1}
}

type dirCache struct {
	mu  sync.Mutex
	m   map[string]dirCached
	cli *http.Client
}

type dirCached struct {
	body []byte
	at   time.Time
	ttl  time.Duration
}

func (s *Server) directoryOn() bool { return s.cfg.DirectoryURL != "" && s.cfg.DirectoryURL != "off" }

var errNoDirectory = errors.New("the station directory is switched off on this box (RADIO_DIRECTORY_URL=off)")

// dirGet fetches a radio-browser path (cached).
func (s *Server) dirGet(ctx context.Context, path string, q url.Values, ttl time.Duration, v any) error {
	if !s.directoryOn() {
		return errNoDirectory
	}
	key := path + "?" + q.Encode()
	s.dir.mu.Lock()
	if c, ok := s.dir.m[key]; ok && time.Since(c.at) < c.ttl {
		s.dir.mu.Unlock()
		return json.Unmarshal(c.body, v)
	}
	if s.dir.cli == nil {
		s.dir.cli = &http.Client{Timeout: 15 * time.Second}
	}
	cli := s.dir.cli
	s.dir.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.DirectoryURL+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", s.cfg.UserAgent)
	res, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("the station directory did not answer: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("the station directory answered HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return err
	}
	s.dir.mu.Lock()
	if len(s.dir.m) > 300 {
		for k, c := range s.dir.m {
			if time.Since(c.at) > c.ttl {
				delete(s.dir.m, k)
			}
		}
		if len(s.dir.m) > 300 {
			s.dir.m = map[string]dirCached{}
		}
	}
	s.dir.m[key] = dirCached{body: b, at: time.Now(), ttl: ttl}
	s.dir.mu.Unlock()
	return nil
}

// directoryClick tells the directory a station was played (it ranks by
// that); fire and forget.
func (s *Server) directoryClick(uuid string) {
	if uuid == "" || !s.directoryOn() || s.cfg.DemoData {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var out any
		_ = s.dirGet(ctx, "/json/url/"+url.PathEscape(uuid), url.Values{}, time.Minute, &out)
	}()
}

// markSaved fills in which results the person already has.
func (s *Server) markSaved(ctx context.Context, uid int64, list []dirStation) {
	mine, _ := s.store.ListStations(ctx, uid, store.StationFilter{})
	byDir := map[string]store.Station{}
	byURL := map[string]store.Station{}
	for _, st := range mine {
		if st.DirectoryID != "" {
			byDir[st.DirectoryID] = st
		}
		byURL[st.URL] = st
	}
	for i := range list {
		st, ok := byDir[list[i].ID]
		if !ok {
			st, ok = byURL[list[i].URL]
		}
		if ok {
			list[i].SavedID, list[i].Favorite = st.ID, st.Favorite
		}
	}
}

// handleDirectorySearch: ?q (name), tag, country (code), language, order
// (popular|votes|name|new), offset, limit.
func (s *Server) handleDirectorySearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	qs := r.URL.Query()
	q := url.Values{"hidebroken": {"true"}, "limit": {"40"}}
	if n := queryInt(r, "limit"); n > 0 && n <= 100 {
		q.Set("limit", strconv.Itoa(n))
	}
	if n := queryInt(r, "offset"); n > 0 {
		q.Set("offset", strconv.Itoa(n))
	}
	if v := strings.TrimSpace(qs.Get("q")); v != "" {
		q.Set("name", v)
	}
	if v := strings.TrimSpace(qs.Get("tag")); v != "" {
		q.Set("tag", strings.ToLower(v))
	}
	if v := strings.TrimSpace(qs.Get("country")); len(v) == 2 {
		q.Set("countrycode", strings.ToUpper(v))
	}
	if v := strings.TrimSpace(qs.Get("language")); v != "" {
		q.Set("language", strings.ToLower(v))
	}
	switch qs.Get("order") {
	case "votes":
		q.Set("order", "votes")
		q.Set("reverse", "true")
	case "name":
		q.Set("order", "name")
	case "new":
		q.Set("order", "changetimestamp")
		q.Set("reverse", "true")
	default:
		q.Set("order", "clickcount")
		q.Set("reverse", "true")
	}
	var raw []rbStation
	if err := s.dirGet(ctx, "/json/stations/search", q, 10*time.Minute, &raw); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "stations": []dirStation{}})
		return
	}
	out := make([]dirStation, 0, len(raw))
	for _, x := range raw {
		if strings.TrimSpace(x.Name) == "" || x.URL == "" {
			continue
		}
		out = append(out, x.view())
	}
	s.markSaved(ctx, actorID(ctx), out)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "stations": out})
}

// handleDirectoryTags lists the most used genres (tags).
func (s *Server) handleDirectoryTags(w http.ResponseWriter, r *http.Request) {
	var raw []struct {
		Name  string `json:"name"`
		Count int    `json:"stationcount"`
	}
	q := url.Values{"order": {"stationcount"}, "reverse": {"true"}, "hidebroken": {"true"}, "limit": {"120"}}
	if err := s.dirGet(r.Context(), "/json/tags", q, 24*time.Hour, &raw); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "tags": []any{}})
		return
	}
	type tag struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	out := []tag{}
	for _, t := range raw {
		n := strings.TrimSpace(t.Name)
		if n == "" || len(n) > 30 || t.Count < 20 {
			continue
		}
		out = append(out, tag{n, t.Count})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tags": out})
}

// handleDirectoryCountries lists the countries with stations.
func (s *Server) handleDirectoryCountries(w http.ResponseWriter, r *http.Request) {
	var raw []struct {
		Name  string `json:"name"`
		Code  string `json:"iso_3166_1"`
		Count int    `json:"stationcount"`
	}
	if err := s.dirGet(r.Context(), "/json/countries", url.Values{"hidebroken": {"true"}}, 24*time.Hour, &raw); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "countries": []any{}})
		return
	}
	type country struct {
		Code  string `json:"code"`
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	out := []country{}
	for _, c := range raw {
		if len(c.Code) == 2 && c.Count > 0 {
			out = append(out, country{strings.ToUpper(c.Code), c.Name, c.Count})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "countries": out})
}

// ---- station logos ----
//
// Logos come from all over the web, many over plain http. The server
// fetches each once, keeps it on disk (DATA_DIR/logos) and serves it from
// there, so the page stays https and stations aren't asked on every visit.

var logoTypes = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp", "image/svg+xml": ".svg",
	"image/x-icon": ".ico", "image/vnd.microsoft.icon": ".ico", "image/avif": ".avif"}

func (s *Server) handleLogo(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("u"))
	if !validStreamURL(raw) {
		writeErr(w, http.StatusBadRequest, "u must be an http(s) address")
		return
	}
	sum := sha256.Sum256([]byte(raw))
	name := hex.EncodeToString(sum[:16])
	dir := filepath.Join(s.cfg.DataDir, "logos")
	serve := func(path, ct string) {
		w.Header().Set("Cache-Control", "private, max-age=604800")
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeFile(w, r, path)
	}
	for ct, ext := range logoTypes {
		if p := filepath.Join(dir, name+ext); fileExists(p) {
			serve(p, ct)
			return
		}
	}
	if fi, err := os.Stat(filepath.Join(dir, name+".fail")); err == nil && time.Since(fi.ModTime()) < 24*time.Hour {
		writeErr(w, http.StatusNotFound, "no logo")
		return
	}
	fail := func() {
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, name+".fail"), nil, 0o644)
		writeErr(w, http.StatusNotFound, "no logo")
	}
	if s.cfg.DemoData && strings.Contains(raw, "example.") {
		fail()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	res, err := s.outGet(ctx, s.outClient(12*time.Second), raw, map[string]string{"Accept": "image/*"})
	if err != nil {
		fail()
		return
	}
	defer res.Body.Close()
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(res.Header.Get("Content-Type"), ";", 2)[0]))
	ext, ok := logoTypes[ct]
	if res.StatusCode != http.StatusOK || !ok {
		fail()
		return
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20+1))
	if err != nil || len(b) == 0 || len(b) > 1<<20 {
		fail()
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, name+ext)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil || os.Rename(tmp, p) != nil {
		writeErr(w, http.StatusInternalServerError, "could not keep the logo")
		return
	}
	serve(p, ct)
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// pruneLogos drops logos nobody asked for in 60 days.
func (s *Server) pruneLogos() {
	dir := filepath.Join(s.cfg.DataDir, "logos")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		fi, err := e.Info()
		if err != nil {
			continue
		}
		age := time.Since(fi.ModTime())
		if age > 60*24*time.Hour || (strings.HasSuffix(e.Name(), ".fail") && age > 24*time.Hour) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
