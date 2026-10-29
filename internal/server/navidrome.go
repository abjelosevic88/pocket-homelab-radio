package server

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// ---- the music library (Navidrome) ----
//
// Channels play from the person's own Navidrome through its Subsonic API.
// Radio asks Core for the person's own Navidrome login (service connection
// "navidrome") or uses a URL, user and password from Settings, and streams
// through itself so no player ever sees them.

type navidrome struct {
	base, user, pass, via string
	http                  *http.Client
}

// navidromeFor is how Radio reaches a person's Navidrome.
func (s *Server) navidromeFor(ctx context.Context, uid int64) (*navidrome, error) {
	st, err := s.store.GetSettings(ctx, uid)
	if err != nil {
		return nil, err
	}
	u, err := s.store.GetUser(ctx, uid)
	if err != nil {
		return nil, err
	}
	switch s.connMode(st.NavidromeMode, st.NavidromePassword) {
	case modeOff:
		return nil, &notConnected{"your music library is switched off in Settings → Connected apps"}
	case modeDirect:
		if st.NavidromeURL == "" || st.NavidromeUser == "" || st.NavidromePassword == "" {
			return nil, &notConnected{"your music library is not set up (Settings → Connected apps): add the Navidrome address, a user and a password, or use Pocket Core"}
		}
		return &navidrome{base: strings.TrimRight(st.NavidromeURL, "/"), user: st.NavidromeUser, pass: st.NavidromePassword, via: modeDirect, http: &http.Client{Timeout: 30 * time.Second}}, nil
	}
	if !s.core.Configured() {
		return nil, &notConnected{"Navidrome through Pocket Core: there is no Pocket Core on this box; use a direct connection"}
	}
	cred, err := s.core.ServiceFor(ctx, u.Email, "navidrome")
	if err != nil {
		var le *core.LinkError
		if errors.As(err, &le) {
			msg := le.Error()
			switch le.Reason {
			case "no_account":
				msg = "you have no Navidrome login in Pocket Core yet (Core → My logins → Navidrome)"
			case "off_admin":
				msg = "the Radio → Navidrome connection is switched off in Pocket Core (Links)"
			case "off_person":
				msg = "you switched the Radio → Navidrome connection off in Pocket Core"
			}
			return nil, &notConnected{"your music library is not set up: " + msg}
		}
		return nil, err
	}
	user := firstNonEmpty(cred.Fields["USER"], cred.Fields["user"], cred.Fields["username"])
	pass := firstNonEmpty(cred.Fields["PASSWORD"], cred.Fields["password"], cred.Key)
	if cred.URL == "" || user == "" || pass == "" {
		return nil, &notConnected{"your music library is not set up: Pocket Core has no Navidrome user and password for you"}
	}
	return &navidrome{base: strings.TrimRight(cred.URL, "/"), user: user, pass: pass, via: modeCore, http: &http.Client{Timeout: 30 * time.Second}}, nil
}

func (n *navidrome) params(extra url.Values) url.Values {
	salt := store.RandomToken(6)
	sum := md5.Sum([]byte(n.pass + salt))
	q := url.Values{"u": {n.user}, "t": {hex.EncodeToString(sum[:])}, "s": {salt}, "v": {"1.16.1"}, "c": {"pocket-radio"}, "f": {"json"}}
	for k, v := range extra {
		q[k] = v
	}
	return q
}

// call runs a Subsonic API method and decodes subsonic-response into v.
func (n *navidrome) call(ctx context.Context, method string, extra url.Values, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.base+"/rest/"+method+"?"+n.params(extra).Encode(), nil)
	if err != nil {
		return err
	}
	res, err := n.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Navidrome: HTTP %d", res.StatusCode)
	}
	var env struct {
		R json.RawMessage `json:"subsonic-response"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<20)).Decode(&env); err != nil {
		return err
	}
	var head struct {
		Status string `json:"status"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(env.R, &head)
	if head.Status != "ok" {
		if head.Error != nil {
			return fmt.Errorf("Navidrome: %s", head.Error.Message)
		}
		return errors.New("Navidrome: request failed")
	}
	return json.Unmarshal(env.R, v)
}

// navSong is a song as the Subsonic API sends it.
type navSong struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	AlbumID  string `json:"albumId"`
	CoverArt string `json:"coverArt"`
	Genre    string `json:"genre"`
	Genres   []struct {
		Name string `json:"name"`
	} `json:"genres"`
	Year     int    `json:"year"`
	Duration int    `json:"duration"`
	Starred  string `json:"starred"`
}

func (x navSong) genreList() []string {
	var out []string
	if x.Genre != "" {
		out = append(out, x.Genre)
	}
	for _, g := range x.Genres {
		if g.Name != "" && !strings.EqualFold(g.Name, x.Genre) {
			out = append(out, g.Name)
		}
	}
	return out
}

type navGenre struct {
	Name      string `json:"value"`
	SongCount int    `json:"songCount"`
}

func (n *navidrome) genres(ctx context.Context) ([]navGenre, error) {
	var out struct {
		Genres struct {
			Genre []navGenre `json:"genre"`
		} `json:"genres"`
	}
	err := n.call(ctx, "getGenres", nil, &out)
	return out.Genres.Genre, err
}

func (n *navidrome) randomSongs(ctx context.Context, size int, genre string, fromYear, toYear int) ([]navSong, error) {
	q := url.Values{"size": {strconv.Itoa(size)}}
	if genre != "" {
		q.Set("genre", genre)
	}
	if fromYear > 0 {
		q.Set("fromYear", strconv.Itoa(fromYear))
	}
	if toYear > 0 {
		q.Set("toYear", strconv.Itoa(toYear))
	}
	var out struct {
		R struct {
			Song []navSong `json:"song"`
		} `json:"randomSongs"`
	}
	err := n.call(ctx, "getRandomSongs", q, &out)
	return out.R.Song, err
}

func (n *navidrome) starred(ctx context.Context) ([]navSong, error) {
	var out struct {
		R struct {
			Song []navSong `json:"song"`
		} `json:"starred2"`
	}
	err := n.call(ctx, "getStarred2", nil, &out)
	return out.R.Song, err
}

type navPlaylist struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SongCount int    `json:"songCount"`
	Duration  int    `json:"duration"`
}

func (n *navidrome) playlists(ctx context.Context) ([]navPlaylist, error) {
	var out struct {
		R struct {
			Playlist []navPlaylist `json:"playlist"`
		} `json:"playlists"`
	}
	err := n.call(ctx, "getPlaylists", nil, &out)
	return out.R.Playlist, err
}

func (n *navidrome) playlistSongs(ctx context.Context, id string) ([]navSong, error) {
	var out struct {
		R struct {
			Entry []navSong `json:"entry"`
		} `json:"playlist"`
	}
	err := n.call(ctx, "getPlaylist", url.Values{"id": {id}}, &out)
	return out.R.Entry, err
}

type navArtist struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	AlbumCount int    `json:"albumCount"`
}

func (n *navidrome) searchArtists(ctx context.Context, q string, limit int) ([]navArtist, error) {
	var out struct {
		R struct {
			Artist []navArtist `json:"artist"`
		} `json:"searchResult3"`
	}
	err := n.call(ctx, "search3", url.Values{"query": {q}, "artistCount": {strconv.Itoa(limit)}, "albumCount": {"0"}, "songCount": {"0"}}, &out)
	return out.R.Artist, err
}

// artistSongs is every song of an artist (up to 40 albums).
func (n *navidrome) artistSongs(ctx context.Context, name string) ([]navSong, error) {
	hits, err := n.searchArtists(ctx, name, 10)
	if err != nil {
		return nil, err
	}
	var id string
	for _, a := range hits {
		if strings.EqualFold(strings.TrimSpace(a.Name), strings.TrimSpace(name)) {
			id = a.ID
			break
		}
	}
	if id == "" {
		return nil, nil
	}
	var ar struct {
		R struct {
			Album []struct {
				ID string `json:"id"`
			} `json:"album"`
		} `json:"artist"`
	}
	if err := n.call(ctx, "getArtist", url.Values{"id": {id}}, &ar); err != nil {
		return nil, err
	}
	var songs []navSong
	for i, al := range ar.R.Album {
		if i >= 40 {
			break
		}
		var out struct {
			R struct {
				Song []navSong `json:"song"`
			} `json:"album"`
		}
		if err := n.call(ctx, "getAlbum", url.Values{"id": {al.ID}}, &out); err != nil {
			return nil, err
		}
		songs = append(songs, out.R.Song...)
	}
	return songs, nil
}

type navRadio struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Stream   string `json:"streamUrl"`
	Homepage string `json:"homePageUrl"`
}

func (n *navidrome) internetRadios(ctx context.Context) ([]navRadio, error) {
	var out struct {
		R struct {
			Station []navRadio `json:"internetRadioStation"`
		} `json:"internetRadioStations"`
	}
	err := n.call(ctx, "getInternetRadioStations", nil, &out)
	return out.R.Station, err
}

// stream opens one song as MP3 at the bitrate (no timeout: it plays for minutes).
func (n *navidrome) stream(ctx context.Context, id string, bitrate int) (*http.Response, error) {
	q := n.params(url.Values{"id": {id}, "format": {"mp3"}, "maxBitRate": {strconv.Itoa(bitrate)}, "estimateContentLength": {"false"}})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.base+"/rest/stream?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	res, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, err
	}
	if ct := res.Header.Get("Content-Type"); res.StatusCode != http.StatusOK || strings.Contains(ct, "json") || strings.Contains(ct, "xml") {
		res.Body.Close()
		return nil, fmt.Errorf("Navidrome would not play the song (HTTP %d)", res.StatusCode)
	}
	return res, nil
}

// handleCover passes a song's or album's cover through: GET /api/v1/cover/{id}.
func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	n, err := s.navidromeFor(ctx, actorID(ctx))
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	size := queryInt(r, "size")
	if size <= 0 || size > 1200 {
		size = 300
	}
	q := n.params(url.Values{"id": {chiParam(r, "id")}, "size": {strconv.Itoa(size)}})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.base+"/rest/getCoverArt?"+q.Encode(), nil)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := n.http.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	defer res.Body.Close()
	ct := res.Header.Get("Content-Type")
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(ct, "image/") {
		writeErr(w, http.StatusNotFound, "no cover")
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = io.Copy(w, io.LimitReader(res.Body, 8<<20))
}

func (s *Server) libraryError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "not_connected": isNotConnected(err)})
}

// handleLibraryInfo is what the channel editor offers: genres (with song
// counts) and playlists. GET /api/v1/library.
func (s *Server) handleLibraryInfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	n, err := s.navidromeFor(ctx, actorID(ctx))
	if err != nil {
		s.libraryError(w, err)
		return
	}
	gs, err := n.genres(ctx)
	if err != nil {
		s.libraryError(w, err)
		return
	}
	pls, _ := n.playlists(ctx)
	if gs == nil {
		gs = []navGenre{}
	}
	if pls == nil {
		pls = []navPlaylist{}
	}
	sortGenres(gs)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "via": n.via, "genres": gs, "playlists": pls})
}

// handleLibraryArtists finds artists for the channel editor: ?q=.
func (s *Server) handleLibraryArtists(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	n, err := s.navidromeFor(ctx, actorID(ctx))
	if err != nil {
		s.libraryError(w, err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "artists": []navArtist{}})
		return
	}
	as, err := n.searchArtists(ctx, q, 12)
	if err != nil {
		s.libraryError(w, err)
		return
	}
	if as == nil {
		as = []navArtist{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "artists": as})
}
