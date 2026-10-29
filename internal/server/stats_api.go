package server

import (
	"net/http"
	"sort"
	"time"

	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// ---- listening overview and history ----

// localDay is the local date of an RFC 3339 time.
func localDay(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ""
	}
	return t.In(time.Local).Format("2006-01-02")
}

// dayBounds is the UTC range of local days [from, to].
func dayBounds(from, to string) (string, string) {
	f, _ := time.ParseInLocation("2006-01-02", from, time.Local)
	t, _ := time.ParseInLocation("2006-01-02", to, time.Local)
	return f.UTC().Format(time.RFC3339Nano), t.AddDate(0, 0, 1).UTC().Format(time.RFC3339Nano)
}

type topEntry struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	ID      int64  `json:"id"`
	Seconds int    `json:"seconds"`
	Count   int    `json:"count"`
}

func topOf(ls []store.Listen, n int) []topEntry {
	by := map[string]*topEntry{}
	for _, l := range ls {
		kind, id := "station", int64(0)
		if l.StationID != nil {
			id = *l.StationID
		} else if l.ChannelID != nil {
			kind, id = "channel", *l.ChannelID
		}
		key := kind + "|" + l.Name
		e := by[key]
		if e == nil {
			e = &topEntry{Name: l.Name, Kind: kind, ID: id}
			by[key] = e
		}
		e.Seconds += l.Seconds
		e.Count++
		if e.ID == 0 {
			e.ID = id
		}
	}
	out := make([]topEntry, 0, len(by))
	for _, e := range by {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Seconds != out[j].Seconds {
			return out[i].Seconds > out[j].Seconds
		}
		return out[i].Name < out[j].Name
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	favs, err := s.store.ListStations(ctx, uid, store.StationFilter{Favorites: true})
	if err != nil {
		s.fail(w, err, "dashboard")
		return
	}
	recent, _ := s.store.ListStations(ctx, uid, store.StationFilter{Recent: true, Limit: 8})
	chs, _ := s.store.ListChannels(ctx, uid)
	channels := make([]map[string]any, 0, len(chs))
	for i := range chs {
		channels = append(channels, s.channelView(r, &chs[i]))
	}
	heard, _ := s.store.ListHeard(ctx, uid, store.HeardFilter{Limit: 6})
	today := store.Today()
	weekFrom := store.AddDays(today, -6)
	f, t := dayBounds(weekFrom, today)
	week, _ := s.store.ListListens(ctx, uid, f, t, 0)
	todaySecs, weekSecs := 0, 0
	for _, l := range week {
		weekSecs += l.Seconds
		if localDay(l.StartedAt) == today {
			todaySecs += l.Seconds
		}
	}
	failing := 0
	for _, st := range favs {
		if st.Health == "failing" {
			failing++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"playing": s.liveFor(uid), "favorites": favs, "recent": recent, "channels": channels,
		"heard": heard, "today_seconds": todaySecs, "week_seconds": weekSecs, "week_top": topOf(week, 3), "failing": failing})
}

// handleHistory is the listening over a range: per day, per hour of the
// day, the top stations and the listens themselves. ?from&to (local
// dates) or ?days=N (default 30).
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	days := queryInt(r, "days")
	if days <= 0 || days > 3660 {
		days = 30
	}
	from, to := dateRange(r, days)
	f, t := dayBounds(from, to)
	ls, err := s.store.ListListens(ctx, uid, f, t, 0)
	if err != nil {
		s.fail(w, err, "history")
		return
	}
	perDay := map[string]int{}
	hours := make([]int, 24)
	total := 0
	for _, l := range ls {
		total += l.Seconds
		perDay[localDay(l.StartedAt)] += l.Seconds
		if st, err := time.Parse(time.RFC3339Nano, l.StartedAt); err == nil {
			hours[st.In(time.Local).Hour()] += l.Seconds
		}
	}
	type day struct {
		Date    string `json:"date"`
		Seconds int    `json:"seconds"`
	}
	series := []day{}
	for d := from; d <= to; d = store.AddDays(d, 1) {
		series = append(series, day{d, perDay[d]})
	}
	heard, _ := s.store.ListHeard(ctx, uid, store.HeardFilter{From: f, To: t, Limit: 500})
	saved := 0
	for _, h := range heard {
		if h.Saved {
			saved++
		}
	}
	listens := ls
	if len(listens) > 200 {
		listens = listens[:200]
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "total_seconds": total, "sessions": len(ls), "days": series,
		"hours": hours, "top": topOf(ls, 10), "listens": listens, "heard_count": len(heard), "saved_count": saved})
}

// handleDay is one day for Pocket Day and dashboards: what was on, for how
// long, and the songs saved that day.
func (s *Server) handleDay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	date := chiParam(r, "date")
	if !validDate(date) || date == "" {
		writeErr(w, http.StatusBadRequest, "the date looks like 2026-10-29")
		return
	}
	f, t := dayBounds(date, date)
	ls, err := s.store.ListListens(ctx, uid, f, t, 0)
	if err != nil {
		s.fail(w, err, "day")
		return
	}
	total := 0
	for _, l := range ls {
		total += l.Seconds
	}
	heard, _ := s.store.ListHeard(ctx, uid, store.HeardFilter{From: f, To: t, Limit: 500})
	saved := []store.Heard{}
	for _, h := range heard {
		if h.Saved {
			saved = append(saved, h)
		}
	}
	sort.Slice(ls, func(i, j int) bool { return ls[i].StartedAt < ls[j].StartedAt })
	writeJSON(w, http.StatusOK, map[string]any{"date": date, "seconds": total, "minutes": total / 60, "listens": ls, "top": topOf(ls, 3),
		"heard_count": len(heard), "saved": saved})
}

// handleDeleteListen removes a listen from the history.
func (s *Server) handleDeleteListen(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.store.DeleteListen(ctx, actorID(ctx), idParam(r, "id")); err != nil {
		s.fail(w, err, "listen")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
