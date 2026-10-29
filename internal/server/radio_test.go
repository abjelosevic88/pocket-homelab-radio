package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-radio/internal/config"
	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

type app struct {
	t     *testing.T
	url   string
	s     *Server
	st    *store.Store
	alice string
	bob   string
	ids   map[string]int64
}

func newApp(t *testing.T, tweak func(*config.Config)) *app {
	t.Helper()
	cfg := testConfig(t.TempDir())
	cfg.BaseURL = "http://radio.test"
	cfg.MaxBodyBytes = 64 << 20
	cfg.DirectoryURL = "off"
	if tweak != nil {
		tweak(cfg)
	}
	st := openTestStore(t, cfg)
	ctx := context.Background()
	a := &app{t: t, st: st, ids: map[string]int64{}}
	for _, u := range []*store.User{{Email: "alice@example.com", Name: "Alice", Role: "admin"}, {Email: "bob@example.com", Name: "Bob", Role: "member"}} {
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		tok, _, _ := st.CreateAPIToken(ctx, u.ID, "t")
		a.ids[u.Name] = u.ID
		if u.Name == "Alice" {
			a.alice = tok
		} else {
			a.bob = tok
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	a.s = New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := a.s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(a.s.Router())
	t.Cleanup(ts.Close)
	a.url = ts.URL
	return a
}

func (a *app) call(auth, method, path string, body any) (int, map[string]any, []any) {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		j, _ := json.Marshal(body)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, a.url+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	var l []any
	if json.Unmarshal(raw, &m) != nil {
		_ = json.Unmarshal(raw, &l)
	}
	return res.StatusCode, m, l
}

func (a *app) ok(auth, method, path string, body any) map[string]any {
	a.t.Helper()
	code, m, _ := a.call(auth, method, path, body)
	if code >= 300 {
		a.t.Fatalf("%s %s → %d %v", method, path, code, m)
	}
	return m
}

func (a *app) list(auth, path string) []any {
	a.t.Helper()
	code, m, l := a.call(auth, "GET", path, nil)
	if code >= 300 {
		a.t.Fatalf("GET %s → %d %v", path, code, m)
	}
	return l
}

func (a *app) expect(want int, auth, method, path string, body any) map[string]any {
	a.t.Helper()
	code, m, _ := a.call(auth, method, path, body)
	if code != want {
		a.t.Fatalf("%s %s → %d, want %d (%v)", method, path, code, want, m)
	}
	return m
}

func (a *app) raw(auth, path string, hdr map[string]string) *http.Response {
	a.t.Helper()
	req, _ := http.NewRequest("GET", a.url+path, nil)
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	return res
}

func num(m map[string]any, k string) float64 { v, _ := m[k].(float64); return v }

func local(c *config.Config) { c.LocalStreams = true }

// a directory station: added without opening the stream
var paradise = map[string]any{"name": "Radio Paradise", "url": "https://stream.radioparadise.com/mp3-192", "directory_id": "abc-123", "tags": "Rock, Eclectic", "favorite": true}

func TestStationsPerPerson(t *testing.T) {
	a := newApp(t, nil)
	st := a.expect(201, a.alice, "POST", "/api/v1/stations", paradise)
	id := int64(num(st, "id"))
	if st["tags"] != "rock, eclectic" || st["source"] != "directory" {
		t.Fatalf("station = %v", st)
	}
	// the same station again: no second row
	again := a.expect(200, a.alice, "POST", "/api/v1/stations", paradise)
	if int64(num(again, "id")) != id {
		t.Fatal("adding the same station twice made two")
	}
	path := fmt.Sprintf("/api/v1/stations/%d", id)
	a.expect(404, a.bob, "GET", path, nil)
	a.expect(404, a.bob, "PUT", path, map[string]any{"name": "Mine now"})
	a.expect(404, a.bob, "DELETE", path, nil)
	a.expect(404, a.bob, "POST", path+"/check", nil)
	if res := a.raw(a.bob, fmt.Sprintf("/stream/station/%d", id), nil); res.StatusCode != 404 {
		t.Fatalf("Bob streamed Alice's station: %d", res.StatusCode)
	}
	if l := a.list(a.bob, "/api/v1/stations?view=all"); len(l) != 0 {
		t.Fatalf("Bob sees %d stations", len(l))
	}
	a.ok(a.alice, "PUT", path, map[string]any{"name": "Paradise", "favorite": false})
	if l := a.list(a.alice, "/api/v1/stations"); len(l) != 0 {
		t.Fatal("a station taken out of the favourites is still listed as one")
	}
	if l := a.list(a.alice, "/api/v1/stations?view=all"); len(l) != 1 {
		t.Fatal("the station should still be there for 'recently played'")
	}
	a.expect(400, a.alice, "POST", "/api/v1/stations", map[string]any{"name": "x", "url": "ftp://nope"})
	a.expect(204, a.alice, "DELETE", path, nil)
}

func TestTypedAddressIsCheckedAndLocalIsRefused(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("icy-name", "Test FM")
		w.Header().Set("icy-genre", "Jazz/Soul")
		w.Header().Set("icy-br", "128")
		_, _ = w.Write(bytes.Repeat([]byte{0xFF}, 2048))
	}))
	defer up.Close()

	// by default the box never fetches local addresses
	a := newApp(t, nil)
	m := a.expect(422, a.alice, "POST", "/api/v1/stations", map[string]any{"url": up.URL + "/live"})
	if !strings.Contains(fmt.Sprint(m["error"]), "private network") {
		t.Fatalf("error = %v", m["error"])
	}
	// "keep it anyway" still saves it
	a.expect(201, a.alice, "POST", "/api/v1/stations", map[string]any{"url": up.URL + "/live", "force": true})

	b := newApp(t, local)
	st := b.expect(201, b.alice, "POST", "/api/v1/stations", map[string]any{"url": up.URL + "/live"})
	if st["name"] != "Test FM" || st["codec"] != "MP3" || num(st, "bitrate") != 128 || st["tags"] != "jazz, soul" {
		t.Fatalf("probe did not fill the station: %v", st)
	}
}

func TestProbeFollowsPlaylistFiles(t *testing.T) {
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/radio.pls", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/x-scpls")
		fmt.Fprintf(w, "[playlist]\nNumberOfEntries=1\nFile1=%s/radio.m3u\nTitle1=Test\n", base)
	})
	mux.HandleFunc("/radio.m3u", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "#EXTM3U\n#EXTINF:-1,Test\n%s/stream\n", base)
	})
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/aac")
		_, _ = w.Write(make([]byte, 4096))
	})
	mux.HandleFunc("/hls.m3u8", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,\nseg1.aac\n")
	})
	up := httptest.NewServer(mux)
	defer up.Close()
	base = up.URL
	a := newApp(t, local)
	p, err := a.s.probeStream(context.Background(), up.URL+"/radio.pls")
	if err != nil || p.URL != up.URL+"/stream" || p.Codec != "AAC" {
		t.Fatalf("probe = %+v, %v", p, err)
	}
	p, err = a.s.probeStream(context.Background(), up.URL+"/hls.m3u8")
	if err != nil || !p.HLS {
		t.Fatalf("HLS probe = %+v, %v", p, err)
	}
}

func TestRelayTakesTitlesOutOfTheStream(t *testing.T) {
	meta := []byte("StreamTitle='Nina Simone - Feeling Good';")
	block := append([]byte{byte((len(meta) + 15) / 16)}, meta...)
	block = append(block, make([]byte, 1+((len(meta)+15)/16)*16-len(block))...)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Icy-MetaData") != "1" {
			t.Error("the relay should ask for titles")
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("icy-metaint", "8")
		var b bytes.Buffer
		b.WriteString("AAAAAAAA")
		b.Write(block)
		b.WriteString("BBBBBBBB")
		b.WriteByte(0)
		b.WriteString("CCCC")
		_, _ = w.Write(b.Bytes())
	}))
	defer up.Close()
	a := newApp(t, local)
	st := a.expect(201, a.alice, "POST", "/api/v1/stations", map[string]any{"name": "Jazz", "url": up.URL + "/live", "directory_id": "x1"})
	res := a.raw(a.alice, fmt.Sprintf("/stream/station/%d?sid=test-session-1", int64(num(st, "id"))), nil)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "AAAAAAAABBBBBBBBCCCC" {
		t.Fatalf("relayed %q", body)
	}
	heard := a.list(a.alice, "/api/v1/heard")
	if len(heard) != 1 {
		t.Fatalf("heard = %v", heard)
	}
	h := heard[0].(map[string]any)
	if h["artist"] != "Nina Simone" || h["song"] != "Feeling Good" || h["source"] != "Jazz" {
		t.Fatalf("heard = %v", h)
	}
	if l := a.list(a.bob, "/api/v1/heard"); len(l) != 0 {
		t.Fatal("Bob sees Alice's songs")
	}
	hid := int64(num(h, "id"))
	a.expect(404, a.bob, "PUT", fmt.Sprintf("/api/v1/heard/%d", hid), map[string]any{"saved": true})
	a.ok(a.alice, "PUT", fmt.Sprintf("/api/v1/heard/%d", hid), map[string]any{"saved": true})
	if l := a.list(a.alice, "/api/v1/heard?saved=1"); len(l) != 1 {
		t.Fatal("saved song missing")
	}
}

func TestStreamTitleParsing(t *testing.T) {
	cases := map[string]string{
		"StreamTitle='Artist - Song';StreamUrl='';": "Artist - Song",
		"StreamTitle='It's Alright';":               "It's Alright",
		"StreamTitle='';":                           "",
	}
	for in, want := range cases {
		got, ok := streamTitle([]byte(in + "\x00\x00"))
		if !ok || got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	if got, _ := streamTitle([]byte("StreamTitle='Caf\xe9 del Mar';")); got != "Café del Mar" {
		t.Errorf("latin-1 title = %q", got)
	}
	if a, s := splitTitle("Miles Davis – So What"); a != "Miles Davis" || s != "So What" {
		t.Errorf("split = %q %q", a, s)
	}
	if usefulTitle("Radio X", "Radio X") || usefulTitle("Advertisement", "") || !usefulTitle("Air - Playground Love", "") {
		t.Error("usefulTitle")
	}
}

func TestPlaylistParsing(t *testing.T) {
	pls := parsePlaylist([]byte("[playlist]\nFile2=https://b.example/2\nTitle2=Two\nFile1=http://a.example/1\nTitle1=One\nNumberOfEntries=2\n"))
	if len(pls) != 2 || pls[0].Name != "One" || pls[1].URL != "https://b.example/2" {
		t.Fatalf("pls = %+v", pls)
	}
	m3u := parsePlaylist([]byte("\ufeff#EXTM3U\n#EXTINF:-1 tvg-logo=\"https://l.example/x.png\" group-title=\"Jazz, Soul\",Smooth, Jazz FM\nhttps://j.example/live\n\nnot a url\nhttp://plain.example/x\n"))
	if len(m3u) != 2 || m3u[0].Name != "Smooth, Jazz FM" || m3u[0].Logo != "https://l.example/x.png" || m3u[0].Tags != "Jazz, Soul" || m3u[1].URL != "http://plain.example/x" {
		t.Fatalf("m3u = %+v", m3u)
	}
	if !isHLS([]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nx.m3u8")) || isHLS([]byte("#EXTM3U\nhttp://x/y")) {
		t.Fatal("isHLS")
	}
}

func TestImportAndExportM3U(t *testing.T) {
	a := newApp(t, nil)
	file := "#EXTM3U\n#EXTINF:-1 group-title=\"news\",News 24\nhttps://news.example/live.mp3\n#EXTINF:-1,Jazz\nhttps://jazz.example/s\n"
	m := a.ok(a.alice, "POST", "/api/v1/stations/import", map[string]any{"text": file})
	if num(m, "added") != 2 {
		t.Fatalf("import = %v", m)
	}
	m = a.ok(a.alice, "POST", "/api/v1/stations/import", map[string]any{"text": file})
	if num(m, "added") != 0 || num(m, "skipped") != 2 {
		t.Fatalf("second import = %v", m)
	}
	res := a.raw(a.alice, "/api/v1/stations/export.m3u", nil)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.HasPrefix(string(b), "#EXTM3U") || !strings.Contains(string(b), "group-title=\"news\",News 24\nhttps://news.example/live.mp3") {
		t.Fatalf("export = %s", b)
	}
	if l := a.list(a.bob, "/api/v1/stations"); len(l) != 0 {
		t.Fatal("import went to the wrong person")
	}
}

func TestBlockedAddresses(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.10", "172.16.0.1", "100.111.226.27", "169.254.1.1", "::1", "fd00::1", "0.0.0.0"} {
		if !blockedIP(net.ParseIP(ip)) {
			t.Errorf("%s should be blocked", ip)
		}
	}
	for _, ip := range []string{"1.1.1.1", "8.8.8.8", "2a00:1450:4001::1"} {
		if blockedIP(net.ParseIP(ip)) {
			t.Errorf("%s should be allowed", ip)
		}
	}
}

// mp3Frame is one silent MPEG-1 layer III frame, 128 kbit/s, 44.1 kHz.
func mp3Frame() []byte {
	f := make([]byte, 417)
	copy(f, []byte{0xFF, 0xFB, 0x90, 0x00})
	return f
}

func TestMP3Frames(t *testing.T) {
	var b bytes.Buffer
	b.Write([]byte{'I', 'D', '3', 3, 0, 0, 0, 0, 0, 20})
	b.Write(make([]byte, 20))
	xing := mp3Frame()
	copy(xing[36:], "Xing")
	b.Write(xing)
	b.Write([]byte{0, 1, 2}) // junk between frames
	for i := 0; i < 3; i++ {
		b.Write(mp3Frame())
	}
	b.WriteString("TAG")
	r := newMP3Reader(&b)
	n := 0
	for {
		f, d, err := r.next()
		if err != nil {
			break
		}
		if len(f) != 417 || d < 26*time.Millisecond || d > 27*time.Millisecond {
			t.Fatalf("frame %d: %d bytes, %v", n, len(f), d)
		}
		n++
	}
	if n != 3 {
		t.Fatalf("read %d frames, want 3 (ID3 and Xing skipped)", n)
	}
}

// fakeNavidrome answers the Subsonic calls a channel makes.
func fakeNavidrome(t *testing.T) *httptest.Server {
	songs := []map[string]any{}
	for i := 1; i <= 6; i++ {
		songs = append(songs, map[string]any{"id": fmt.Sprintf("s%d", i), "title": fmt.Sprintf("Song %d", i), "artist": fmt.Sprintf("Artist %d", (i+1)/2),
			"album": "Album", "albumId": "al1", "genre": map[bool]string{true: "Jazz", false: "Rock"}[i <= 4], "year": 1960 + i, "duration": 180})
	}
	ok := func(w http.ResponseWriter, body map[string]any) {
		body["status"] = "ok"
		body["version"] = "1.16.1"
		_ = json.NewEncoder(w).Encode(map[string]any{"subsonic-response": body})
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("u") != "nav" {
			ok(w, map[string]any{"status": "failed"})
			return
		}
		switch strings.TrimPrefix(r.URL.Path, "/rest/") {
		case "getGenres":
			ok(w, map[string]any{"genres": map[string]any{"genre": []map[string]any{{"value": "Jazz", "songCount": 4}, {"value": "Rock", "songCount": 2}}}})
		case "getRandomSongs":
			g := r.URL.Query().Get("genre")
			var out []map[string]any
			for _, s := range songs {
				if g == "" || s["genre"] == g {
					out = append(out, s)
				}
			}
			ok(w, map[string]any{"randomSongs": map[string]any{"song": out}})
		case "getPlaylists":
			ok(w, map[string]any{"playlists": map[string]any{"playlist": []map[string]any{{"id": "p1", "name": "Mix", "songCount": 2}}}})
		case "getInternetRadioStations":
			ok(w, map[string]any{"internetRadioStations": map[string]any{"internetRadioStation": []map[string]any{{"id": "r1", "name": "Navi FM", "streamUrl": "https://navi.example/fm"}}}})
		case "stream":
			w.Header().Set("Content-Type", "audio/mpeg")
			for i := 0; i < 400; i++ { // ~10 s of sound
				_, _ = w.Write(mp3Frame())
			}
		default:
			ok(w, map[string]any{})
		}
	}))
}

func connectNavidrome(t *testing.T, a *app, auth, url string) {
	a.ok(auth, "PUT", "/api/v1/settings", map[string]any{"navidrome_mode": "direct", "navidrome_url": url, "navidrome_user": "nav", "navidrome_password": "secret"})
}

func TestChannelsPerPersonAndPicking(t *testing.T) {
	nav := fakeNavidrome(t)
	defer nav.Close()
	a := newApp(t, nil)
	// without a library a channel cannot play
	c := a.expect(201, a.alice, "POST", "/api/v1/channels", map[string]any{"name": "Jazz", "rules": map[string]any{"genres": []string{"Jazz", "Jazz", ""}, "avoid_hours": 24}})
	id := int64(num(c, "id"))
	rules := c["rules"].(map[string]any)
	if len(rules["genres"].([]any)) != 1 || rules["artists"] == nil {
		t.Fatalf("rules not cleaned: %v", rules)
	}
	if res := a.raw(a.alice, fmt.Sprintf("/stream/channel/%d", id), nil); res.StatusCode != 503 {
		t.Fatalf("stream without a library = %d", res.StatusCode)
	}
	path := fmt.Sprintf("/api/v1/channels/%d", id)
	a.expect(404, a.bob, "GET", path, nil)
	a.expect(404, a.bob, "PUT", path, map[string]any{"name": "x"})
	a.expect(404, a.bob, "DELETE", path, nil)
	a.expect(404, a.bob, "GET", path+"/now", nil)
	a.expect(400, a.alice, "POST", "/api/v1/channels", map[string]any{"name": "Bad", "bitrate": 100})

	connectNavidrome(t, a, a.alice, nav.URL)
	pv := a.ok(a.alice, "POST", "/api/v1/channels/preview", map[string]any{"rules": map[string]any{"genres": []string{"Jazz"}}})
	if pv["ok"] != true || len(pv["songs"].([]any)) != 4 || num(pv, "count") != 4 {
		t.Fatalf("preview = %v", pv)
	}
	pv = a.ok(a.alice, "POST", "/api/v1/channels/preview", map[string]any{"rules": map[string]any{"genres": []string{"Jazz"}, "year_from": 1963}})
	if len(pv["songs"].([]any)) != 2 {
		t.Fatalf("year filter: %v", pv["songs"])
	}
	// Bob has no library: Alice's connection is hers alone
	if m := a.ok(a.bob, "POST", "/api/v1/channels/preview", map[string]any{"rules": map[string]any{}}); m["ok"] != false {
		t.Fatal("Bob used Alice's Navidrome")
	}

	// four jazz songs, no repeats within a day: four different songs, then the oldest again
	ch, _ := a.st.GetChannel(context.Background(), a.ids["Alice"], id)
	n, _ := a.s.navidromeFor(context.Background(), a.ids["Alice"])
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		s, err := a.s.pickSong(context.Background(), n, ch)
		if err != nil || seen[s.ID] || s.Genre != "Jazz" {
			t.Fatalf("pick %d = %+v %v (seen %v)", i, s, err, seen)
		}
		seen[s.ID] = true
		_ = a.st.ChannelPlayed(context.Background(), ch.UserID, ch.ID, s.ID, s.Artist)
		time.Sleep(2 * time.Millisecond)
	}
	if _, err := a.s.pickSong(context.Background(), n, ch); err != nil {
		t.Fatal("a channel with everything played lately should still play")
	}

	// the listen address: a playlist file and an MP3 stream with titles
	tok := strings.TrimSuffix(strings.TrimPrefix(c["listen_url"].(string), "http://radio.test/listen/"), ".mp3")
	res := a.raw("", "/listen/"+tok+".m3u", nil)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(b), c["listen_url"].(string)) {
		t.Fatalf("m3u = %s", b)
	}
	if res := a.raw("", "/listen/nothing-like-a-token.mp3", nil); res.StatusCode != 404 {
		t.Fatal("unknown listen address should 404")
	}
	res = a.raw("", "/listen/"+tok+".mp3", map[string]string{"Icy-MetaData": "1"})
	if res.StatusCode != 200 || res.Header.Get("icy-metaint") != "16000" || res.Header.Get("icy-name") != "Jazz" {
		t.Fatalf("listen = %d %v", res.StatusCode, res.Header)
	}
	head := make([]byte, 16001)
	if _, err := io.ReadFull(res.Body, head); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if head[0] != 0xFF || head[16000] == 0 {
		t.Fatalf("stream starts %x, title length byte %d", head[:4], head[16000])
	}
	now := a.ok(a.alice, "GET", path+"/now", nil)
	if now["song"] == nil {
		t.Fatalf("now = %v", now)
	}
	// a new address: the old one stops working
	c2 := a.ok(a.alice, "POST", path+"/address", nil)
	if c2["listen_url"] == c["listen_url"] {
		t.Fatal("address not replaced")
	}
	if res := a.raw("", "/listen/"+tok+".m3u", nil); res.StatusCode != 404 {
		t.Fatal("the old address still works")
	}
}

func TestImportFromNavidrome(t *testing.T) {
	nav := fakeNavidrome(t)
	defer nav.Close()
	a := newApp(t, nil)
	connectNavidrome(t, a, a.alice, nav.URL)
	m := a.ok(a.alice, "POST", "/api/v1/stations/import/navidrome", nil)
	if num(m, "added") != 1 {
		t.Fatalf("navidrome import = %v", m)
	}
	l := a.list(a.alice, "/api/v1/stations")
	if len(l) != 1 || l[0].(map[string]any)["source"] != "navidrome" {
		t.Fatalf("stations = %v", l)
	}
}

func TestHistoryPerPerson(t *testing.T) {
	a := newApp(t, nil)
	ctx := context.Background()
	start := time.Now().Add(-2 * time.Hour)
	for i, secs := range []int{1800, 600} {
		st := start.Add(time.Duration(i) * 40 * time.Minute)
		l := store.Listen{Name: "FIP", StartedAt: st.UTC().Format(time.RFC3339Nano), EndedAt: st.Add(time.Duration(secs) * time.Second).UTC().Format(time.RFC3339Nano), Seconds: secs}
		if err := a.st.AddListen(ctx, a.ids["Alice"], &l); err != nil {
			t.Fatal(err)
		}
	}
	h := a.ok(a.alice, "GET", "/api/v1/history?days=7", nil)
	if num(h, "total_seconds") != 2400 || num(h, "sessions") != 2 || len(h["days"].([]any)) != 7 {
		t.Fatalf("history = %v", h)
	}
	top := h["top"].([]any)[0].(map[string]any)
	if top["name"] != "FIP" || num(top, "seconds") != 2400 {
		t.Fatalf("top = %v", top)
	}
	if b := a.ok(a.bob, "GET", "/api/v1/history?days=7", nil); num(b, "total_seconds") != 0 {
		t.Fatal("Bob sees Alice's listening")
	}
	day := a.ok(a.alice, "GET", "/api/v1/day/"+localDay(start.UTC().Format(time.RFC3339Nano)), nil)
	if num(day, "minutes") < 10 {
		t.Fatalf("day = %v", day)
	}
	lid := int64(num(h["listens"].([]any)[0].(map[string]any), "id"))
	a.expect(404, a.bob, "DELETE", fmt.Sprintf("/api/v1/listens/%d", lid), nil)
	a.expect(204, a.alice, "DELETE", fmt.Sprintf("/api/v1/listens/%d", lid), nil)
}

func TestListenBeatsForHLS(t *testing.T) {
	a := newApp(t, nil)
	st := a.expect(201, a.alice, "POST", "/api/v1/stations", map[string]any{"name": "BBC", "url": "https://bbc.example/live.m3u8", "hls": true, "directory_id": "bbc"})
	id := num(st, "id")
	a.expect(404, a.bob, "POST", "/api/v1/listens/beat", map[string]any{"sid": "beat-session-1", "station_id": id})
	a.ok(a.alice, "POST", "/api/v1/listens/beat", map[string]any{"sid": "beat-session-1", "station_id": id})
	now := a.ok(a.alice, "GET", "/api/v1/now?sid=beat-session-1", nil)
	if now["playing"] != true || now["name"] != "BBC" {
		t.Fatalf("now = %v", now)
	}
	if b := a.ok(a.bob, "GET", "/api/v1/now?sid=beat-session-1", nil); b["playing"] != false {
		t.Fatal("Bob sees Alice's player")
	}
	a.ok(a.alice, "POST", "/api/v1/listens/beat", map[string]any{"sid": "beat-session-1", "stop": true})
	if now := a.ok(a.alice, "GET", "/api/v1/now?sid=beat-session-1", nil); now["playing"] != false {
		t.Fatal("stop did not end the listen")
	}
	if res := a.raw(a.alice, fmt.Sprintf("/stream/station/%d", int64(id)), nil); res.StatusCode != 409 {
		t.Fatal("an HLS station is not relayed")
	}
}

func TestExportImportIsIdempotent(t *testing.T) {
	a := newApp(t, func(c *config.Config) { c.DemoData = true })
	res := a.raw(a.alice, "/api/v1/export.json", nil)
	doc, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var exp exportDoc
	if err := json.Unmarshal(doc, &exp); err != nil || len(exp.Stations) < 10 || len(exp.Channels) != 3 || len(exp.Listens) < 50 {
		t.Fatalf("export: %v, %d stations %d channels %d listens", err, len(exp.Stations), len(exp.Channels), len(exp.Listens))
	}
	var raw map[string]any
	_ = json.Unmarshal(doc, &raw)
	first := a.ok(a.bob, "POST", "/api/v1/import", raw)
	if int(num(first, "stations")) != len(exp.Stations) || num(first, "channels") != 3 || int(num(first, "listens")) != len(exp.Listens) {
		t.Fatalf("first import = %v", first)
	}
	second := a.ok(a.bob, "POST", "/api/v1/import", raw)
	for _, k := range []string{"stations", "channels", "listens", "heard"} {
		if num(second, k) != 0 {
			t.Fatalf("second import added %s: %v", k, second)
		}
	}
	// Alice's demo data is still hers alone
	if d := a.ok(a.alice, "GET", "/api/v1/dashboard", nil); len(d["favorites"].([]any)) != 10 || len(d["channels"].([]any)) != 3 {
		t.Fatalf("dashboard = %v", d)
	}
}

func TestSettingsPerPerson(t *testing.T) {
	a := newApp(t, nil)
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"country": "de", "sleep_minutes": 45, "navidrome_password": "pw", "title": "Our Radio"})
	al := a.ok(a.alice, "GET", "/api/v1/settings", nil)
	if al["country"] != "DE" || num(al, "sleep_minutes") != 45 || al["navidrome_password"] != "••••" || al["title"] != "Our Radio" {
		t.Fatalf("alice = %v", al)
	}
	a.ok(a.bob, "PUT", "/api/v1/settings", map[string]any{"title": "Bob's"})
	bo := a.ok(a.bob, "GET", "/api/v1/settings", nil)
	if bo["country"] != "" || num(bo, "sleep_minutes") != 30 || bo["navidrome_password"] != "" || bo["title"] != "Our Radio" {
		t.Fatalf("bob = %v", bo)
	}
	a.expect(400, a.alice, "PUT", "/api/v1/settings", map[string]any{"country": "Germany"})
	a.expect(400, a.alice, "PUT", "/api/v1/settings", map[string]any{"channel_bitrate": 99})
}

func TestLinkScopes(t *testing.T) {
	get := func(p string) *http.Request { r, _ := http.NewRequest("GET", p, nil); return r }
	post := func(p string) *http.Request { r, _ := http.NewRequest("POST", p, nil); return r }
	read := []string{"radio:read"}
	for _, p := range []string{"/api/v1/day/2026-10-29", "/api/v1/dashboard", "/api/v1/history", "/api/v1/stations", "/api/v1/stations/3", "/api/v1/heard", "/api/v1/now"} {
		if !linkAllows(read, get(p)) {
			t.Errorf("read should allow %s", p)
		}
	}
	if linkAllows(read, post("/api/v1/stations")) || linkAllows(read, get("/api/v1/settings")) || linkAllows(read, get("/api/v1/export.json")) {
		t.Fatal("read must not write, nor see settings or exports")
	}
	if linkAllows(read, get("/stream/station/1")) || linkAllows(read, get("/api/v1/logo")) {
		t.Fatal("links do not play or fetch")
	}
	write := []string{"radio:write"}
	if !linkAllows(write, post("/api/v1/stations")) || linkAllows(write, get("/api/v1/stations")) || linkAllows(write, &http.Request{Method: "PUT", URL: get("/api/v1/stations/1").URL}) {
		t.Fatal("write may add stations, nothing else")
	}
}
