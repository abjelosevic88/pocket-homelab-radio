package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-radio/internal/config"
	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// capScopes are the link tokens of the fake Core: pcx_<who>_<what> acts for
// <who>@example.com with one scope.
var capScopes = map[string]string{
	"search": core.ScopeSearch, "day": core.ScopeDay, "widget": core.ScopeWidget, "capture": core.ScopeCapture,
	"read": "radio:read", "write": "radio:write",
}

// capApp is newApp behind a fake Core that knows the capScopes tokens and
// keeps the manifest the app registers with.
func capApp(t *testing.T, tweaks ...func(*config.Config)) (*app, func() map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var manifest map[string]any
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/register":
			mu.Lock()
			manifest = map[string]any{}
			_ = json.NewDecoder(r.Body).Decode(&manifest)
			mu.Unlock()
			io.WriteString(w, `{"token":"pca_test"}`)
		case "/api/v1/auth/introspect":
			var in struct {
				Token string `json:"token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			parts := strings.SplitN(strings.TrimPrefix(in.Token, "pcx_"), "_", 2)
			if len(parts) != 2 || capScopes[parts[1]] == "" {
				io.WriteString(w, `{"active":false}`)
				return
			}
			email := parts[0] + "@example.com"
			fmt.Fprintf(w, `{"active":true,"kind":"delegated","scope":%q,"audience":"radio","user":{"id":1,"sub":%q,"email":%q,"name":%q,"role":"member"},"app":{"id":7,"slug":"search","name":"Pocket Search"}}`,
				capScopes[parts[1]], email, email, parts[0])
		default:
			io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(coreSrv.Close)
	a := newApp(t, func(c *config.Config) {
		c.CoreURL = coreSrv.URL
		for _, f := range tweaks {
			f(c)
		}
	})
	return a, func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return manifest
	}
}

func (a *app) pocketGet(tok, path string, out any) int {
	a.t.Helper()
	req, _ := http.NewRequest("GET", a.url+"/api/v1/pocket/"+path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode == 200 {
		if err := json.Unmarshal(body, out); err != nil {
			a.t.Fatalf("%s: %v (%s)", path, err, body)
		}
	}
	return res.StatusCode
}

func (a *app) pocketFind(tok, q string) []core.SearchItem {
	a.t.Helper()
	var res core.SearchResults
	if code := a.pocketGet(tok, "search?"+q, &res); code != 200 || res.Items == nil {
		a.t.Fatalf("search %s = %d %+v", q, code, res)
	}
	return res.Items
}

func (a *app) pocketDayOf(tok, date, tz string) core.Day {
	a.t.Helper()
	var d core.Day
	if code := a.pocketGet(tok, "day/"+date+"?tz="+tz, &d); code != 200 || d.Date != date || d.Items == nil {
		a.t.Fatalf("day %s = %d %+v", date, code, d)
	}
	return d
}

func (a *app) pocketWidgetOf(tok string) core.WidgetData {
	a.t.Helper()
	var w core.WidgetData
	if code := a.pocketGet(tok, "widget", &w); code != 200 || w.Items == nil {
		a.t.Fatalf("widget = %d %+v", code, w)
	}
	return w
}

func found(items []core.SearchItem) string {
	var out []string
	for _, it := range items {
		out = append(out, it.Kind+" "+it.Title)
	}
	return strings.Join(out, " | ")
}

func metrics(ms []core.Metric) string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Label+"="+m.Value)
	}
	return strings.Join(out, ", ")
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// keep adds a station for somebody straight to the store.
func (a *app) keep(who string, st store.Station) *store.Station {
	a.t.Helper()
	if st.URL == "" {
		st.URL = "http://radio.example.com/" + strings.ToLower(strings.ReplaceAll(st.Name, " ", "-"))
	}
	if err := a.st.CreateStation(context.Background(), a.ids[who], &st); err != nil {
		a.t.Fatal(err)
	}
	return &st
}

// listen records a stretch of listening to a station or channel.
func (a *app) listen(who string, stationID, channelID int64, name string, at time.Time, secs int) {
	a.t.Helper()
	l := store.Listen{Name: name, StartedAt: stamp(at), EndedAt: stamp(at.Add(time.Duration(secs) * time.Second)), Seconds: secs}
	if stationID > 0 {
		l.StationID = &stationID
	}
	if channelID > 0 {
		l.ChannelID = &channelID
	}
	if err := a.st.AddListen(context.Background(), a.ids[who], &l); err != nil {
		a.t.Fatal(err)
	}
}

// Pocket Search finds a person's own stations and channels by name, genre,
// country and notes, the name that answers best first, and nothing of
// anybody else's; the station directory is never asked.
func TestPocketSearch(t *testing.T) {
	a := newApp(t, nil)
	ctx := context.Background()
	yesterday := stamp(time.Now().AddDate(0, 0, -1))
	fip := a.keep("Alice", store.Station{Name: "FIP", Tags: "jazz, eclectic, world", CountryCode: "FR", Language: "french", Favorite: true, LastPlayedAt: &yesterday})
	a.keep("Alice", store.Station{Name: "FIP Jazz", Tags: "jazz", CountryCode: "FR", Favorite: true})
	once := a.keep("Alice", store.Station{Name: "Radio Swiss Jazz", Tags: "jazz, smooth", CountryCode: "CH", Favorite: false, LastPlayedAt: &yesterday})
	a.keep("Alice", store.Station{Name: "Antena Zagreb", Tags: "pop", CountryCode: "HR", Notes: "for the morning coffee", Favorite: true})
	ch := &store.Channel{Name: "Sunday Jazz", Description: "slow records", Rules: store.ChannelRules{Genres: []string{"Jazz", "Soul"}}, ListenToken: "tok-sunday"}
	if err := a.st.CreateChannel(ctx, a.ids["Alice"], ch); err != nil {
		t.Fatal(err)
	}
	a.keep("Bob", store.Station{Name: "Bob's Jazz FM", Tags: "jazz", CountryCode: "FR", Favorite: true})

	got := a.pocketFind(a.alice, "q=fip")
	if found(got) != "station FIP | station FIP Jazz" {
		t.Fatalf("fip = %s", found(got))
	}
	if it := got[0]; it.ID != fmt.Sprintf("station:%d", fip.ID) || it.URL != "/stations?q=FIP" || it.Snippet != "France · jazz, eclectic, world · listened yesterday" ||
		it.Date != yesterday || it.Score != 4 {
		t.Errorf("FIP = %+v", it)
	}
	if it := got[1]; it.Snippet != "France · jazz" || it.URL != "/stations?q=FIP+Jazz" {
		t.Errorf("FIP Jazz = %+v", it)
	}
	// a genre: the stations in the person's order, the one played once and the channel
	got = a.pocketFind(a.alice, "q=jazz")
	if found(got) != "station FIP Jazz | station Radio Swiss Jazz | channel Sunday Jazz | station FIP" {
		t.Fatalf("jazz = %s", found(got))
	}
	if it := got[1]; it.ID != fmt.Sprintf("station:%d", once.ID) || it.URL != "/" || it.Snippet != "Switzerland · jazz, smooth · listened yesterday" {
		t.Errorf("played once = %+v", it)
	}
	if it := got[2]; it.ID != fmt.Sprintf("channel:%d", ch.ID) || it.URL != fmt.Sprintf("/channels/%d", ch.ID) || it.Snippet != "Your channel · Jazz, Soul" {
		t.Errorf("channel = %+v", it)
	}
	// the country by its name or its code, every word counts, the notes too
	for q, want := range map[string]string{
		"q=switzerland": "station Radio Swiss Jazz", "q=HR": "station Antena Zagreb", "q=croatia": "station Antena Zagreb",
		"q=france+jazz": "station FIP | station FIP Jazz", "q=coffee": "station Antena Zagreb", "q=slow": "channel Sunday Jazz",
		"q=french": "station FIP", "q=jazz+croatia": "",
	} {
		if got := found(a.pocketFind(a.alice, q)); got != want {
			t.Errorf("%s = %q, want %q", q, got, want)
		}
	}
	// the limit, nothing asked, and words without a letter or a digit
	if got := a.pocketFind(a.alice, "q=jazz&limit=2"); len(got) != 2 {
		t.Errorf("limit 2 = %s", found(got))
	}
	for _, q := range []string{"q=", "q=+", "q=%3F%3F", "q=*", "q=%22"} {
		if got := a.pocketFind(a.alice, q); len(got) != 0 {
			t.Errorf("%s = %s", q, found(got))
		}
	}
	// each person finds only their own
	if got := found(a.pocketFind(a.bob, "q=jazz")); got != "station Bob's Jazz FM" {
		t.Errorf("Bob = %s", got)
	}
	for _, q := range []string{"q=fip", "q=sunday", "q=zagreb", "q=coffee"} {
		if got := a.pocketFind(a.bob, q); len(got) != 0 {
			t.Errorf("Bob found Alice's with %s: %s", q, found(got))
		}
	}
	if got := a.pocketFind(a.alice, "q=bob"); len(got) != 0 {
		t.Errorf("Alice found Bob's: %s", found(got))
	}
	// GET /stations is as it was
	if l := a.list(a.alice, "/api/v1/stations?q=jazz"); len(l) != 2 {
		t.Errorf("/stations?q=jazz = %d", len(l))
	}
}

// Pocket Day gets each station and channel of the day once, with its
// minutes, in the caller's time zone; the minutes add up to the day.
func TestPocketDay(t *testing.T) {
	a := newApp(t, nil)
	ctx := context.Background()
	fip := a.keep("Alice", store.Station{Name: "FIP", Tags: "jazz, eclectic, world", CountryCode: "FR", Favorite: true})
	gone := a.keep("Alice", store.Station{Name: "Old Station", Favorite: true})
	ch := &store.Channel{Name: "Sunday Jazz", ListenToken: "tok-sunday"}
	if err := a.st.CreateChannel(ctx, a.ids["Alice"], ch); err != nil {
		t.Fatal(err)
	}
	at := func(clock string) time.Time {
		v, err := time.Parse("2006-01-02 15:04", "2026-11-20 "+clock)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	a.listen("Alice", fip.ID, 0, "FIP", at("08:00"), 1800)
	a.listen("Alice", 0, ch.ID, "Sunday Jazz", at("12:00"), 600)
	a.listen("Alice", fip.ID, 0, "FIP", at("18:00"), 1200)
	a.listen("Alice", gone.ID, 0, "Old Station", at("23:30"), 900)
	if err := a.st.DeleteStation(ctx, a.ids["Alice"], gone.ID); err != nil {
		t.Fatal(err)
	}
	for i, title := range []string{"Miles Davis - So What", "Nina Simone - Feeling Good"} {
		h := store.Heard{StationID: &fip.ID, Source: "FIP", Title: title, Saved: i == 1, HeardAt: stamp(at("08:10").Add(time.Duration(i) * time.Minute))}
		if _, err := a.st.AddHeard(ctx, a.ids["Alice"], &h); err != nil {
			t.Fatal(err)
		}
	}

	d := a.pocketDayOf(a.alice, "2026-11-20", "UTC")
	if len(d.Items) != 3 || d.Summary != "Listened for 1 h 15 min, mostly FIP" || metrics(d.Metrics) != "Time=1 h 15 min, Songs heard=2, Songs saved=1" {
		t.Fatalf("day = %+v", d)
	}
	if it := d.Items[0]; it.ID != fmt.Sprintf("station:%d", fip.ID) || it.Title != "FIP" || it.Sub != "France · jazz, eclectic" || it.Time != "08:00" ||
		it.End != "" || it.Minutes != 50 || it.URL != "/stations?q=FIP" {
		t.Errorf("FIP = %+v", it)
	}
	if it := d.Items[1]; it.ID != fmt.Sprintf("channel:%d", ch.ID) || it.Title != "Sunday Jazz" || it.Sub != "Your channel" || it.Time != "12:00" ||
		it.End != "12:10" || it.Minutes != 10 || it.URL != fmt.Sprintf("/channels/%d", ch.ID) {
		t.Errorf("channel = %+v", it)
	}
	if it := d.Items[2]; it.ID != "" || it.Title != "Old Station" || it.Time != "23:30" || it.End != "23:45" || it.Minutes != 15 || it.URL != "/history" {
		t.Errorf("deleted station = %+v", it)
	}
	total := 0.0
	for _, it := range d.Items {
		total += it.Minutes
	}
	if total != 75 {
		t.Errorf("minutes add up to %v", total)
	}
	// an hour east the late listening belongs to the next day
	d = a.pocketDayOf(a.alice, "2026-11-21", "Europe/Sarajevo")
	if len(d.Items) != 1 || d.Items[0].Title != "Old Station" || d.Items[0].Time != "00:30" || d.Summary != "Listened to Old Station for 15 min" || metrics(d.Metrics) != "Time=15 min" {
		t.Errorf("Sarajevo, the 21st = %+v", d)
	}
	// under a minute, one station
	a.listen("Alice", fip.ID, 0, "FIP", at("09:00").AddDate(0, 0, 2), 40)
	if d = a.pocketDayOf(a.alice, "2026-11-22", "UTC"); d.Summary != "Listened to FIP" || len(d.Metrics) != 0 || d.Items[0].Minutes != 0.67 {
		t.Errorf("the 22nd = %+v", d)
	}
	// a day with nothing, another person's day, a day that is not one
	if d = a.pocketDayOf(a.alice, "2026-11-19", "UTC"); len(d.Items) != 0 || d.Summary != "" || len(d.Metrics) != 0 {
		t.Errorf("an empty day = %+v", d)
	}
	if d = a.pocketDayOf(a.bob, "2026-11-20", "UTC"); len(d.Items) != 0 || d.Summary != "" {
		t.Errorf("Bob sees Alice's day: %+v", d)
	}
	for _, date := range []string{"2026-13-40", "today", "2026-11-2"} {
		if code := a.pocketGet(a.alice, "day/"+date, &d); code != 400 {
			t.Errorf("day %s = %d, want 400", date, code)
		}
	}
	// GET /day/{date} is as it was
	if day := a.ok(a.alice, "GET", "/api/v1/day/2026-11-20", nil); day["date"] != "2026-11-20" || day["listens"] == nil {
		t.Errorf("/day = %v", day)
	}
}

// Pocket Hub gets what plays now, the stations played lately and then the
// favourites, each once; the line only while something plays.
func TestPocketWidget(t *testing.T) {
	a := newApp(t, nil)
	if w := a.pocketWidgetOf(a.alice); w.Line != "" || len(w.Items) != 0 || len(w.Metrics) != 0 {
		t.Fatalf("nothing kept = %+v", w)
	}
	twoDays := stamp(time.Now().AddDate(0, 0, -2))
	fip := a.keep("Alice", store.Station{Name: "FIP", Tags: "jazz, eclectic, world", Favorite: true, LastPlayedAt: &twoDays})
	once := a.keep("Alice", store.Station{Name: "Radio Swiss Jazz", Tags: "jazz", Favorite: false, LastPlayedAt: &twoDays})
	zagreb := a.keep("Alice", store.Station{Name: "Antena Zagreb", Tags: "pop", Favorite: true})
	if err := a.st.MarkStationPlayed(context.Background(), a.ids["Alice"], once.ID); err != nil {
		t.Fatal(err)
	}
	a.listen("Alice", fip.ID, 0, "FIP", time.Now().AddDate(0, 0, -2), 1500)

	w := a.pocketWidgetOf(a.alice)
	if w.Line != "" || w.URL != "/" || len(w.Items) != 3 || metrics(w.Metrics) != "This week=25 min" {
		t.Fatalf("widget = %+v", w)
	}
	want := []core.WidgetItem{
		{ID: fmt.Sprintf("station:%d", once.ID), Title: "Radio Swiss Jazz", Detail: "played today", URL: "/"},
		{ID: fmt.Sprintf("station:%d", fip.ID), Title: "FIP", Detail: "played 2 days ago", URL: "/stations?q=FIP"},
		{ID: fmt.Sprintf("station:%d", zagreb.ID), Title: "Antena Zagreb", Detail: "pop", URL: "/stations?q=Antena+Zagreb"},
	}
	for i, it := range w.Items {
		if it != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, it, want[i])
		}
	}
	// something plays: it leads, with the song when the station sends one, and is not listed twice
	l := a.s.liveStart(context.Background(), a.ids["Alice"], "station", fip.ID, "FIP", "sid-alice-1", "web")
	w = a.pocketWidgetOf(a.alice)
	if w.Line != "Playing now: FIP" || len(w.Items) != 3 || w.Items[0].Title != "FIP" || w.Items[0].Detail != "playing now" || w.Items[1].Title != "Radio Swiss Jazz" {
		t.Errorf("playing = %+v", w)
	}
	a.s.liveMu.Lock()
	l.title = "Miles Davis - So What"
	a.s.liveMu.Unlock()
	if w = a.pocketWidgetOf(a.alice); w.Line != "Playing now: Miles Davis - So What on FIP" {
		t.Errorf("with a song = %q", w.Line)
	}
	// the other person sees none of it
	if w = a.pocketWidgetOf(a.bob); w.Line != "" || len(w.Items) != 0 || len(w.Metrics) != 0 {
		t.Errorf("Bob's widget = %+v", w)
	}
	a.s.liveMu.Lock()
	delete(a.s.live, l.sid)
	a.s.liveMu.Unlock()
	if w = a.pocketWidgetOf(a.alice); w.Line != "" {
		t.Errorf("stopped = %q", w.Line)
	}
}

// A link token with one of the pocket:* scopes reaches its one route (and
// who it acts for) and nothing else; the older scopes do not open the new routes.
func TestPocketLinkScopes(t *testing.T) {
	a, _ := capApp(t)
	st := a.keep("Alice", store.Station{Name: "FIP", Tags: "jazz", Favorite: true})
	a.listen("Alice", st.ID, 0, "FIP", time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC), 600)
	const day = "/api/v1/pocket/day/2026-11-20"
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{"pcx_alice_search", "GET", "/api/v1/pocket/search?q=fip", 200},
		{"pcx_alice_search", "GET", "/api/v1/auth/me", 200},
		{"pcx_alice_search", "POST", "/api/v1/pocket/search?q=fip", 405}, // reading only: the router has no such route
		{"pcx_alice_search", "GET", day, 403},
		{"pcx_alice_search", "GET", "/api/v1/pocket/widget", 403},
		{"pcx_alice_search", "GET", "/api/v1/stations", 403},
		{"pcx_alice_search", "GET", "/api/v1/directory/search?q=fip", 403},
		{"pcx_alice_search", "GET", "/stream/station/1", 403},
		{"pcx_alice_day", "GET", day, 200},
		{"pcx_alice_day", "GET", day + "?tz=Europe/Sarajevo", 200},
		{"pcx_alice_day", "GET", "/api/v1/auth/me", 200},
		{"pcx_alice_day", "GET", "/api/v1/day/2026-11-20", 403},
		{"pcx_alice_day", "GET", "/api/v1/history", 403},
		{"pcx_alice_day", "GET", "/api/v1/pocket/search?q=fip", 403},
		{"pcx_alice_day", "GET", "/api/v1/pocket/widget", 403},
		{"pcx_alice_day", "DELETE", "/api/v1/listens/1", 403},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/widget", 200},
		{"pcx_alice_widget", "GET", "/api/v1/auth/me", 200},
		{"pcx_alice_widget", "GET", "/api/v1/dashboard", 403},
		{"pcx_alice_widget", "GET", "/api/v1/now", 403},
		{"pcx_alice_widget", "POST", "/api/v1/listens/beat", 403},
		{"pcx_alice_widget", "GET", day, 403},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/search?q=fip", 403},
		// nothing can be kept here: the scope is not offered and there is no route
		{"pcx_alice_capture", "POST", "/api/v1/pocket/capture", 404},
		{"pcx_alice_capture", "GET", "/api/v1/pocket/search?q=fip", 403},
		// the older scopes keep their own routes and do not open the new ones
		{"pcx_alice_read", "GET", "/api/v1/day/2026-11-20", 200},
		{"pcx_alice_read", "GET", day, 403},
		{"pcx_alice_read", "GET", "/api/v1/pocket/widget", 403},
		{"pcx_alice_read", "GET", "/api/v1/pocket/search?q=fip", 403},
		{"pcx_alice_write", "GET", "/api/v1/pocket/search?q=fip", 403},
		{"pcx_alice_write", "POST", "/api/v1/pocket/capture", 404},
	} {
		if code, m, _ := a.call(c.tok, c.method, c.path, map[string]any{}); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%v)", c.tok, c.method, c.path, code, c.want, m)
		}
	}
	// asked of linkAllows itself: each scope reads its one route and writes nothing
	for _, c := range []struct {
		scope, method, path string
		want                bool
	}{
		{core.ScopeSearch, "GET", core.SearchPath, true},
		{core.ScopeSearch, "POST", core.SearchPath, false},
		{core.ScopeDay, "GET", core.DayPath + "2026-11-20", true},
		{core.ScopeDay, "PUT", core.DayPath + "2026-11-20", false},
		{core.ScopeWidget, "GET", core.WidgetPath, true},
		{core.ScopeWidget, "DELETE", core.WidgetPath, false},
		{core.ScopeCapture, "POST", core.CapturePath, false},
		{core.ScopeSearch, "GET", "/api/v1/stations", false},
		{core.ScopeDay, "GET", "/api/v1/day/2026-11-20", false},
		{"radio:read", "GET", core.WidgetPath, false},
		{"radio:write", "GET", core.SearchPath, false},
	} {
		r, _ := http.NewRequest(c.method, c.path, nil)
		if got := linkAllows([]string{c.scope}, r); got != c.want {
			t.Errorf("linkAllows(%s, %s %s) = %v, want %v", c.scope, c.method, c.path, got, c.want)
		}
	}
	// the token answers for its person only
	if got := found(a.pocketFind("pcx_alice_search", "q=fip")); got != "station FIP" {
		t.Errorf("Alice through a link = %s", got)
	}
	if got := a.pocketFind("pcx_bob_search", "q=fip"); len(got) != 0 {
		t.Errorf("Bob through a link found Alice's: %s", found(got))
	}
	if d := a.pocketDayOf("pcx_alice_day", "2026-11-20", "UTC"); len(d.Items) != 1 || d.Items[0].Title != "FIP" {
		t.Errorf("Alice's day through a link = %+v", d)
	}
	if d := a.pocketDayOf("pcx_bob_day", "2026-11-20", "UTC"); len(d.Items) != 0 {
		t.Errorf("Bob's day through a link = %+v", d)
	}
	if w := a.pocketWidgetOf("pcx_alice_widget"); len(w.Items) != 1 || w.Items[0].Title != "FIP" {
		t.Errorf("Alice's widget through a link = %+v", w)
	}
	if w := a.pocketWidgetOf("pcx_bob_widget"); len(w.Items) != 0 {
		t.Errorf("Bob's widget through a link = %+v", w)
	}
	// reading through a link recorded nothing and played nothing
	if h := a.ok(a.alice, "GET", "/api/v1/history?from=2026-11-20&to=2026-11-20", nil); num(h, "sessions") != 1 {
		t.Errorf("listens after the link calls = %v", h["sessions"])
	}
	if got, _ := a.st.GetStation(context.Background(), a.ids["Alice"], st.ID); got.PlayCount != 0 {
		t.Errorf("play count after the link calls = %d", got.PlayCount)
	}
}

// The manifest tells Core the shelf, what can be asked here and the scopes
// behind it; a quick line is not among them.
func TestManifestCapabilities(t *testing.T) {
	a, manifest := capApp(t, func(c *config.Config) { c.CoreRegistrationKey = "pcr_test" })
	if err := a.s.registerWithCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	m := manifest()
	if m["slug"] != "radio" || m["category"] != "Media" {
		t.Fatalf("manifest: %v %v", m["slug"], m["category"])
	}
	search, _ := m["search"].(map[string]any)
	day, _ := m["day"].(map[string]any)
	widget, _ := m["widget"].(map[string]any)
	if search == nil || day == nil || widget == nil || m["capture"] != nil {
		t.Fatalf("capabilities: %v %v %v %v", search, day, widget, m["capture"])
	}
	if fmt.Sprint(search["bangs"]) != "[radio station]" || fmt.Sprint(search["kinds"]) != "[station channel]" || search["what"] == "" {
		t.Errorf("search: %v", search)
	}
	if day["label"] != "Radio" || day["one"] != "station" || day["many"] != "stations" || day["unit"] != "min" || day["what"] == "" {
		t.Errorf("day: %v", day)
	}
	if widget["title"] != "Radio" || widget["what"] == "" {
		t.Errorf("widget: %v", widget)
	}
	offered := map[string]bool{}
	for _, o := range m["offers"].([]any) {
		offered[o.(map[string]any)["scope"].(string)] = true
	}
	for _, sc := range []string{"radio:read", "radio:write", core.ScopeSearch, core.ScopeDay, core.ScopeWidget, core.ScopeMoveExport, core.ScopeMoveImport} {
		if !offered[sc] {
			t.Errorf("%s is not offered", sc)
		}
	}
	if offered[core.ScopeCapture] {
		t.Error("pocket:capture is offered")
	}
}

// The words of the lines: plural, minutes, when it was on.
func TestCapabilityWords(t *testing.T) {
	now := time.Date(2026, 11, 27, 15, 0, 0, 0, time.Local)
	at := func(d time.Time) *string { s := stamp(d); return &s }
	for _, c := range []struct {
		ts   *string
		want string
	}{
		{nil, ""},
		{at(now.Add(-time.Hour)), "today"},
		{at(now.AddDate(0, 0, -1)), "yesterday"},
		{at(now.AddDate(0, 0, -3)), "3 days ago"},
		{at(time.Date(2026, 11, 2, 12, 0, 0, 0, time.Local)), "on 2 Nov"},
		{at(time.Date(2025, 11, 2, 12, 0, 0, 0, time.Local)), "on 2 Nov 2025"},
	} {
		if got := playedWords(c.ts, now); got != c.want {
			t.Errorf("playedWords(%v) = %q, want %q", c.ts, got, c.want)
		}
	}
	for secs, want := range map[int]string{20: "1 min", 90: "2 min", 2700: "45 min", 3600: "1 h", 4800: "1 h 20 min"} {
		if got := humanMinutes(secs); got != want {
			t.Errorf("humanMinutes(%d) = %q, want %q", secs, got, want)
		}
	}
	if count(0, "song", "songs") != "" || count(1, "song", "songs") != "1 song" || count(3, "song", "songs") != "3 songs" {
		t.Error("count")
	}
	if countryName("de") != "Germany" || countryName("QQ") != "QQ" {
		t.Error("countryName")
	}
}
