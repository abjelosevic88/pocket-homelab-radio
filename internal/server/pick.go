package server

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// ---- choosing a channel's songs ----
//
// Playlists and artists are where songs come from (all of them together);
// genres, years, "only starred" and the exclusions narrow them down. With
// no playlists or artists the whole library is the source, asked for random
// songs genre by genre. A song is not played again within AvoidHours, and
// ArtistGap songs pass before the same artist comes back — both relaxed
// when the rules leave nothing else.

type songPool struct {
	key   string
	songs []navSong
	at    time.Time
}

type poolCache struct {
	mu sync.Mutex
	m  map[int64]songPool
}

// explicitSource reports whether the rules name where songs come from.
func explicitSource(r store.ChannelRules) bool {
	return len(r.Playlists) > 0 || len(r.Artists) > 0 || r.StarredOnly
}

func lowerSet(list []string) map[string]bool {
	m := map[string]bool{}
	for _, v := range list {
		m[strings.ToLower(strings.TrimSpace(v))] = true
	}
	return m
}

// songMatches applies the narrowing rules.
func songMatches(x navSong, r store.ChannelRules) bool {
	if x.ID == "" {
		return false
	}
	gs := x.genreList()
	if len(r.Genres) > 0 {
		want, ok := lowerSet(r.Genres), false
		for _, g := range gs {
			if want[strings.ToLower(g)] {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(r.ExcludeGenres) > 0 {
		no := lowerSet(r.ExcludeGenres)
		for _, g := range gs {
			if no[strings.ToLower(g)] {
				return false
			}
		}
	}
	if len(r.ExcludeArtists) > 0 && lowerSet(r.ExcludeArtists)[strings.ToLower(x.Artist)] {
		return false
	}
	if r.YearFrom > 0 && (x.Year == 0 || x.Year < r.YearFrom) {
		return false
	}
	if r.YearTo > 0 && (x.Year == 0 || x.Year > r.YearTo) {
		return false
	}
	if r.StarredOnly && x.Starred == "" {
		return false
	}
	return true
}

// explicitPool gathers the songs of the named sources (cached an hour).
func (s *Server) explicitPool(ctx context.Context, nav *navidrome, c *store.Channel) ([]navSong, error) {
	kb, _ := json.Marshal([]any{c.Rules.Playlists, c.Rules.Artists, c.Rules.StarredOnly, nav.base, nav.user})
	key := string(kb)
	s.pools.mu.Lock()
	if p, ok := s.pools.m[c.ID]; ok && p.key == key && time.Since(p.at) < time.Hour {
		s.pools.mu.Unlock()
		return p.songs, nil
	}
	s.pools.mu.Unlock()
	seen := map[string]bool{}
	var songs []navSong
	add := func(list []navSong) {
		for _, x := range list {
			if x.ID != "" && !seen[x.ID] {
				seen[x.ID] = true
				songs = append(songs, x)
			}
		}
	}
	for _, id := range c.Rules.Playlists {
		list, err := nav.playlistSongs(ctx, id)
		if err != nil {
			return nil, err
		}
		add(list)
	}
	for _, a := range c.Rules.Artists {
		list, err := nav.artistSongs(ctx, a)
		if err != nil {
			return nil, err
		}
		add(list)
	}
	if c.Rules.StarredOnly {
		starred, err := nav.starred(ctx)
		if err != nil {
			return nil, err
		}
		if len(c.Rules.Playlists) == 0 && len(c.Rules.Artists) == 0 {
			add(starred)
		} else {
			// songs inside playlists don't always say they are starred
			on := map[string]bool{}
			for _, x := range starred {
				on[x.ID] = true
			}
			for i := range songs {
				if on[songs[i].ID] && songs[i].Starred == "" {
					songs[i].Starred = "1"
				}
			}
		}
	}
	s.pools.mu.Lock()
	s.pools.m[c.ID] = songPool{key: key, songs: songs, at: time.Now()}
	s.pools.mu.Unlock()
	return songs, nil
}

// candidates are songs the channel may play next (before the repeat rules).
func (s *Server) candidates(ctx context.Context, nav *navidrome, c *store.Channel) ([]navSong, error) {
	var out []navSong
	if explicitSource(c.Rules) {
		pool, err := s.explicitPool(ctx, nav, c)
		if err != nil {
			return nil, err
		}
		for _, x := range pool {
			if songMatches(x, c.Rules) {
				out = append(out, x)
			}
		}
		return out, nil
	}
	genres := append([]string(nil), c.Rules.Genres...)
	rand.Shuffle(len(genres), func(i, j int) { genres[i], genres[j] = genres[j], genres[i] })
	if len(genres) == 0 {
		genres = []string{""}
	}
	for i := 0; i < 4 && len(out) < 20; i++ {
		g := genres[i%len(genres)]
		list, err := nav.randomSongs(ctx, 120, g, c.Rules.YearFrom, c.Rules.YearTo)
		if err != nil {
			return nil, err
		}
		for _, x := range list {
			if songMatches(x, c.Rules) {
				out = append(out, x)
			}
		}
		if len(genres) == 1 && len(list) < 120 {
			break // that is the whole genre
		}
	}
	return out, nil
}

// pickSong chooses the next song for a channel.
func (s *Server) pickSong(ctx context.Context, nav *navidrome, c *store.Channel) (navSong, error) {
	list, err := s.candidates(ctx, nav, c)
	if err != nil {
		return navSong{}, err
	}
	if len(list) == 0 {
		return navSong{}, errNoSongs
	}
	avoid := time.Duration(c.Rules.AvoidHours) * time.Hour
	since := time.Now().Add(-max(avoid, time.Hour)).UTC().Format(time.RFC3339Nano)
	recent, _ := s.store.RecentChannelPlays(ctx, c.UserID, c.ID, since, 5000)
	played := map[string]string{} // song → last played
	cutoff := time.Now().Add(-avoid).UTC().Format(time.RFC3339Nano)
	for _, p := range recent {
		if _, ok := played[p.SongID]; !ok {
			played[p.SongID] = p.PlayedAt
		}
	}
	gapArtists := map[string]bool{}
	for i, p := range recent {
		if i >= c.Rules.ArtistGap {
			break
		}
		gapArtists[strings.ToLower(p.Artist)] = true
	}
	fresh := func(x navSong) bool { at, ok := played[x.ID]; return !ok || avoid == 0 || at < cutoff }
	var best, ok1 []navSong
	for _, x := range list {
		if !fresh(x) {
			continue
		}
		ok1 = append(ok1, x)
		if !gapArtists[strings.ToLower(x.Artist)] {
			best = append(best, x)
		}
	}
	switch {
	case len(best) > 0:
		return best[rand.Intn(len(best))], nil
	case len(ok1) > 0:
		return ok1[rand.Intn(len(ok1))], nil
	}
	// everything was played lately: the one played longest ago
	sort.SliceStable(list, func(i, j int) bool { return played[list[i].ID] < played[list[j].ID] })
	return list[0], nil
}

func sortGenres(gs []navGenre) {
	sort.SliceStable(gs, func(i, j int) bool {
		if gs[i].SongCount != gs[j].SongCount {
			return gs[i].SongCount > gs[j].SongCount
		}
		return strings.ToLower(gs[i].Name) < strings.ToLower(gs[j].Name)
	})
}

// handleChannelPreview shows what rules would play: a sample of songs and
// about how many there are. POST /api/v1/channels/preview {rules}.
func (s *Server) handleChannelPreview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Rules store.ChannelRules `json:"rules"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	in.Rules.Clean()
	uid := actorID(ctx)
	nav, err := s.navidromeFor(ctx, uid)
	if err != nil {
		s.libraryError(w, err)
		return
	}
	c := &store.Channel{ID: -uid, UserID: uid, Rules: in.Rules} // a pool slot of its own per person
	list, err := s.candidates(ctx, nav, c)
	if err != nil {
		s.libraryError(w, err)
		return
	}
	count, estimate := len(list), false
	if !explicitSource(in.Rules) {
		estimate = true
		if gs, err := nav.genres(ctx); err == nil {
			want := lowerSet(in.Rules.Genres)
			count = 0
			for _, g := range gs {
				if len(want) == 0 || want[strings.ToLower(g.Name)] {
					count += g.SongCount
				}
			}
		}
	}
	rand.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
	if len(list) > 12 {
		list = list[:12]
	}
	type song struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Artist string `json:"artist"`
		Album  string `json:"album"`
		Year   int    `json:"year"`
		Genre  string `json:"genre"`
		Cover  string `json:"cover"`
	}
	out := make([]song, 0, len(list))
	for _, x := range list {
		out = append(out, song{x.ID, x.Title, x.Artist, x.Album, x.Year, strings.Join(x.genreList(), ", "), firstNonEmpty(x.CoverArt, x.AlbumID)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "songs": out, "count": count, "estimate": estimate})
}
