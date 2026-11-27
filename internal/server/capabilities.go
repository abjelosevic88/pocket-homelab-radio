package server

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// The three routes behind `capabilities` (links.go): /api/v1/pocket/search,
// /day/{date} and /widget, for Pocket Search, Pocket Day and Pocket Hub. Each
// runs as the request's person and reads only what is theirs; the SDK's
// handlers check and cap what goes in and out. They read what GET /stations,
// /channels, /day/{date} and /dashboard read, and change nothing: nothing is
// played, no listen is recorded, and the station directory is never asked
// (Search is for the stations a person has, not the 50,000 out there).

const (
	widgetRecent = 4 // recently played stations the widget reads
	widgetRows   = 6 // rows it shows
)

// stationURL opens a station where the app shows it: a favourite in "Your
// stations", filtered to its name (a station has no page of its own); one
// played but not kept on Listen, under "Played lately".
func stationURL(st *store.Station) string {
	if !st.Favorite {
		return "/"
	}
	return "/stations?q=" + url.QueryEscape(st.Name)
}

func channelURL(id int64) string { return "/channels/" + strconv.FormatInt(id, 10) }

// dots joins the parts of a line that are there: "Germany · jazz · listened today".
func dots(parts ...string) string {
	out := parts[:0:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// count is a number with its word: "1 song", "3 songs"; nothing for none.
func count(n int, one, many string) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// genres are a station's first few tags: "jazz, blues".
func genres(tags string, n int) string {
	t := store.SplitTags(tags)
	if len(t) > n {
		t = t[:n]
	}
	return strings.Join(t, ", ")
}

// humanMinutes is a length of listening as the pages say it: "45 min", "1 h 20 min".
func humanMinutes(seconds int) string {
	m := int(math.Round(float64(seconds) / 60))
	if m < 1 {
		m = 1
	}
	if h := m / 60; h > 0 {
		if m%60 == 0 {
			return fmt.Sprintf("%d h", h)
		}
		return fmt.Sprintf("%d h %d min", h, m%60)
	}
	return fmt.Sprintf("%d min", m)
}

// playedWords says when something was last on, in the server's time:
// "today", "yesterday", "3 days ago", "on 2 Nov", "on 2 Nov 2025"; nothing
// when it never was.
func playedWords(ts *string, now time.Time) string {
	if ts == nil || *ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, *ts)
	if err != nil {
		return ""
	}
	t, now = t.In(time.Local), now.In(time.Local)
	day := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, time.Local) }
	days := int(math.Round(day(now).Sub(day(t)).Hours() / 24))
	switch {
	case days <= 0:
		return "today"
	case days == 1:
		return "yesterday"
	case days < 7:
		return strconv.Itoa(days) + " days ago"
	case t.Year() == now.Year():
		return "on " + t.Format("2 Jan")
	}
	return "on " + t.Format("2 Jan 2006")
}

// listened puts "listened" before playedWords: "listened yesterday".
func listened(ts *string, now time.Time) string {
	if w := playedWords(ts, now); w != "" {
		return "listened " + w
	}
	return ""
}

// ---------- search ----------

// hasWord reports whether there is a letter or a digit to look for.
func hasWord(q string) bool {
	return strings.IndexFunc(q, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0
}

// matchAll reports whether every word is in the text, or is the country code.
func matchAll(words []string, text, code string) bool {
	for _, w := range words {
		if !strings.Contains(text, w) && (code == "" || w != code) {
			return false
		}
	}
	return true
}

// nameScore says how well a name answers the words: it is the name (4), the
// name starts with them (3), the name has them all (2), or they were found
// in something else (its genres, country or notes) (1).
func nameScore(name string, q string, words []string) float64 {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case n == q:
		return 4
	case strings.HasPrefix(n, q):
		return 3
	case matchAll(words, n, ""):
		return 2
	}
	return 1
}

// pocketSearch answers Pocket Search with the person's own stations (the
// favourites and the ones played lately, all the stations GET /stations?view=all
// lists) and their channels, found by name, genres, country (its name or its
// code), language and notes; every word has to be there. The name that answers
// best comes first; of equally good ones the stations in the person's order,
// then the channels.
func (s *Server) pocketSearch(r *http.Request, q string, limit int) ([]core.SearchItem, error) {
	ctx, uid := r.Context(), actorID(r.Context())
	if !hasWord(q) {
		return nil, nil
	}
	lq := strings.ToLower(q)
	words := strings.Fields(lq)
	stations, err := s.store.ListStations(ctx, uid, store.StationFilter{})
	if err != nil {
		return nil, err
	}
	channels, err := s.store.ListChannels(ctx, uid)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := []core.SearchItem{}
	for i := range stations {
		st := &stations[i]
		country := ""
		if st.CountryCode != "" {
			country = countryName(st.CountryCode)
		}
		text := strings.ToLower(strings.Join([]string{st.Name, st.Tags, country, st.Language, st.Notes}, "\n"))
		if !matchAll(words, text, strings.ToLower(st.CountryCode)) {
			continue
		}
		date := st.UpdatedAt
		if st.LastPlayedAt != nil {
			date = *st.LastPlayedAt
		}
		out = append(out, core.SearchItem{ID: "station:" + strconv.FormatInt(st.ID, 10), Title: st.Name, Kind: "station",
			Snippet: dots(country, genres(st.Tags, 3), listened(st.LastPlayedAt, now)), Date: date, URL: stationURL(st),
			Score: nameScore(st.Name, lq, words)})
	}
	for i := range channels {
		c := &channels[i]
		text := strings.ToLower(strings.Join([]string{c.Name, c.Description, strings.Join(c.Rules.Genres, ", "), strings.Join(c.Rules.Artists, ", ")}, "\n"))
		if !matchAll(words, text, "") {
			continue
		}
		date := c.UpdatedAt
		if c.LastPlayedAt != nil {
			date = *c.LastPlayedAt
		}
		g := c.Rules.Genres
		if len(g) > 3 {
			g = g[:3]
		}
		out = append(out, core.SearchItem{ID: "channel:" + strconv.FormatInt(c.ID, 10), Title: c.Name, Kind: "channel",
			Snippet: dots("Your channel", strings.Join(g, ", "), listened(c.LastPlayedAt, now)), Date: date, URL: channelURL(c.ID),
			Score: nameScore(c.Name, lq, words)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---------- day ----------

// dayStation is one station (or channel) on a day: every stretch of it together.
type dayStation struct {
	key, kind string
	id        int64
	name      string       // as it is called now, else as it was then
	first     store.Listen // the earliest stretch
	stretches int
	seconds   int
}

// pocketDay answers Pocket Day from the listening kept here (what GET
// /day/{date} reads): each station or channel listened to that day once,
// with when it was first on and the minutes it got, so the minutes add up
// to the day's listening. The day runs in the caller's time zone when it
// gives one, else in the server's, as the app's own pages do.
func (s *Server) pocketDay(r *http.Request, date string, tz *time.Location) (*core.Day, error) {
	ctx, uid := r.Context(), actorID(r.Context())
	loc := time.Local
	if tz != nil {
		loc = tz
	}
	d, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return nil, err
	}
	from, to := d.UTC().Format(time.RFC3339Nano), d.AddDate(0, 0, 1).UTC().Format(time.RFC3339Nano)
	ls, err := s.store.ListListens(ctx, uid, from, to, 0)
	if err != nil {
		return nil, err
	}
	if len(ls) == 0 {
		return nil, nil
	}

	// one line per station or channel, the way the top lists tell them apart
	var list []*dayStation
	byKey := map[string]*dayStation{}
	total := 0
	for i := len(ls) - 1; i >= 0; i-- { // oldest first
		l := ls[i]
		x := &dayStation{kind: "station", name: l.Name, first: l}
		switch {
		case l.StationID != nil:
			x.id = *l.StationID
			x.key = "station:" + strconv.FormatInt(x.id, 10)
		case l.ChannelID != nil:
			x.kind, x.id = "channel", *l.ChannelID
			x.key = "channel:" + strconv.FormatInt(x.id, 10)
		default: // the station is gone: its name is what is left
			x.key = "name:" + strings.ToLower(l.Name)
		}
		if have := byKey[x.key]; have != nil {
			x = have
		} else {
			byKey[x.key] = x
			list = append(list, x)
		}
		x.stretches++
		x.seconds += l.Seconds
		total += l.Seconds
	}

	day := &core.Day{Date: date}
	clock := func(ts string) (time.Time, bool) {
		t, err := time.Parse(time.RFC3339Nano, ts)
		return t.In(loc), err == nil
	}
	for _, x := range list {
		it := core.DayItem{Title: x.name, Minutes: math.Round(float64(x.seconds)/60*100) / 100, URL: "/history"}
		switch {
		case x.kind == "channel":
			it.ID, it.Sub, it.URL = x.key, "Your channel", channelURL(x.id)
			if c, err := s.store.GetChannel(ctx, uid, x.id); err == nil {
				it.Title = c.Name
			}
		case x.id > 0:
			it.ID = x.key
			if st, err := s.store.GetStation(ctx, uid, x.id); err == nil {
				it.Title, it.URL = st.Name, stationURL(st)
				if st.CountryCode != "" {
					it.Sub = dots(countryName(st.CountryCode), genres(st.Tags, 2))
				} else {
					it.Sub = genres(st.Tags, 2)
				}
			}
		}
		if t, ok := clock(x.first.StartedAt); ok {
			it.Time = t.Format("15:04")
			// one stretch that ended the same day: when it ended too
			if e, ok := clock(x.first.EndedAt); ok && x.stretches == 1 && e.Format("2006-01-02") == t.Format("2006-01-02") {
				it.End = e.Format("15:04")
			}
		}
		x.name = it.Title
		day.Items = append(day.Items, it)
	}

	// "Listened to Radio Paradise for 45 min", "Listened for 2 h 10 min, mostly Radio Paradise"
	sort.SliceStable(list, func(i, j int) bool { return list[i].seconds > list[j].seconds })
	top := list[0].name
	long := total >= 60
	switch {
	case len(list) == 1 && long:
		day.Summary = fmt.Sprintf("Listened to %s for %s", top, humanMinutes(total))
	case len(list) == 1:
		day.Summary = "Listened to " + top
	case long:
		day.Summary = fmt.Sprintf("Listened for %s, mostly %s", humanMinutes(total), top)
	default:
		day.Summary = "Listened to " + count(len(list), "station", "stations")
	}
	if long {
		day.Metrics = append(day.Metrics, core.Metric{Label: "Time", Value: humanMinutes(total)})
	}
	heard, _ := s.store.ListHeard(ctx, uid, store.HeardFilter{From: from, To: to, Limit: 500})
	saved := 0
	for _, h := range heard {
		if h.Saved {
			saved++
		}
	}
	if len(heard) > 0 {
		day.Metrics = append(day.Metrics, core.Metric{Label: "Songs heard", Value: strconv.Itoa(len(heard))})
	}
	if saved > 0 {
		day.Metrics = append(day.Metrics, core.Metric{Label: "Songs saved", Value: strconv.Itoa(saved)})
	}
	return day, nil
}

// ---------- widget ----------

// playingNow is the person's newest stream playing now, if any (kept in
// memory by the server while it plays a station or a channel out).
func (s *Server) playingNow(uid int64) *liveStream {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	var newest *liveStream
	for _, l := range s.live {
		if l.uid == uid && (newest == nil || l.started.After(newest.started)) {
			c := *l
			newest = &c
		}
	}
	return newest
}

// pocketWidget answers Pocket Hub with what plays now (the line, only while
// this server plays something out for the person), the stations played
// lately and then the favourites, each once. The numbers are today's and the
// last seven days' listening, as Listen shows them. All of it is read here:
// the newest few stations, the first favourites and one week of listening.
// Someone with no stations and nothing playing gets nothing to show.
func (s *Server) pocketWidget(r *http.Request) (*core.WidgetData, error) {
	ctx, uid := r.Context(), actorID(r.Context())
	recent, err := s.store.ListStations(ctx, uid, store.StationFilter{Recent: true, Limit: widgetRecent})
	if err != nil {
		return nil, err
	}
	favs, err := s.store.ListStations(ctx, uid, store.StationFilter{Favorites: true, Limit: widgetRows})
	if err != nil {
		return nil, err
	}
	live := s.playingNow(uid)
	if len(recent) == 0 && len(favs) == 0 && live == nil {
		return nil, nil
	}
	w := &core.WidgetData{URL: "/"}
	seen := map[string]bool{}
	add := func(key string, it core.WidgetItem) {
		if seen[key] || len(w.Items) == widgetRows {
			return
		}
		seen[key] = true
		w.Items = append(w.Items, it)
	}
	if live != nil {
		w.Line = "Playing now: " + live.name
		if live.title != "" {
			w.Line = "Playing now: " + live.title + " on " + live.name
		}
		key := live.kind + ":" + strconv.FormatInt(live.id, 10)
		it := core.WidgetItem{ID: key, Title: live.name, Detail: "playing now", URL: channelURL(live.id)}
		if live.kind == "station" {
			it.URL = "/"
			if st, err := s.store.GetStation(ctx, uid, live.id); err == nil {
				it.URL = stationURL(st)
			}
		}
		add(key, it)
	}
	now := time.Now()
	for i := range recent {
		st := &recent[i]
		key := "station:" + strconv.FormatInt(st.ID, 10)
		add(key, core.WidgetItem{ID: key, Title: st.Name, Detail: "played " + playedWords(st.LastPlayedAt, now), URL: stationURL(st)})
	}
	for i := range favs {
		st := &favs[i]
		key := "station:" + strconv.FormatInt(st.ID, 10)
		add(key, core.WidgetItem{ID: key, Title: st.Name, Detail: genres(st.Tags, 2), URL: stationURL(st)})
	}

	today := store.Today()
	f, t := dayBounds(store.AddDays(today, -6), today)
	week, err := s.store.ListListens(ctx, uid, f, t, 0)
	if err != nil {
		return nil, err
	}
	todaySecs, weekSecs := 0, 0
	for _, l := range week {
		weekSecs += l.Seconds
		if localDay(l.StartedAt) == today {
			todaySecs += l.Seconds
		}
	}
	if todaySecs > 0 {
		w.Metrics = append(w.Metrics, core.Metric{Label: "Today", Value: humanMinutes(todaySecs)})
	}
	if weekSecs > 0 {
		w.Metrics = append(w.Metrics, core.Metric{Label: "This week", Value: humanMinutes(weekSecs)})
	}
	return w, nil
}
