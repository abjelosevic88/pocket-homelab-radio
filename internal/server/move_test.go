package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-radio/internal/config"
)

// moveLinks starts the app behind a fake Core whose pcx_<who>_<export|import|read>
// tokens are Pocket Move links acting for alice@ or bob@ (both members to Core).
func moveLinks(t *testing.T, tweaks ...func(*config.Config)) *app {
	t.Helper()
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/auth/introspect" {
			io.WriteString(w, `{}`)
			return
		}
		var in struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		parts := strings.SplitN(strings.TrimPrefix(in.Token, "pcx_"), "_", 2)
		if len(parts) != 2 {
			io.WriteString(w, `{"active":false}`)
			return
		}
		scope := map[string]string{"export": core.ScopeMoveExport, "import": core.ScopeMoveImport, "read": "radio:read"}[parts[1]]
		email := parts[0] + "@example.com"
		fmt.Fprintf(w, `{"active":true,"kind":"delegated","scope":%q,"audience":"radio","user":{"id":1,"sub":%q,"email":%q,"name":%q,"role":"member"},"app":{"id":7,"slug":"move","name":"Pocket Move"}}`, scope, email, email, parts[0])
	}))
	t.Cleanup(coreSrv.Close)
	return newApp(t, func(c *config.Config) {
		c.CoreURL = coreSrv.URL
		for _, f := range tweaks {
			f(c)
		}
	})
}

func (a *app) moveRaw(tok, method, path, ctype string, body []byte) (int, []byte) {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.url+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

// moveImport posts data the way Pocket Move does: in the multipart field form,
// or as the raw body with ctype when form is empty.
func (a *app) moveImport(tok, path, form, ctype string, data []byte) map[string]any {
	a.t.Helper()
	body := data
	if form != "" {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile(form, "upload")
		_, _ = fw.Write(data)
		_ = mw.Close()
		ctype, body = mw.FormDataContentType(), buf.Bytes()
	}
	code, out := a.moveRaw(tok, "POST", path, ctype, body)
	if code != 200 && code != 201 {
		a.t.Fatalf("POST %s → %d %s", path, code, out)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	return m
}

// moveScopes checks what each Move link may reach.
type moveCase struct {
	tok, method, path string
	want              int
}

func (a *app) checkMoveScopes(cases []moveCase) {
	a.t.Helper()
	for _, c := range cases {
		if code, body := a.moveRaw(c.tok, c.method, c.path, "application/json", []byte("{}")); code != c.want {
			a.t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, code, c.want, body)
		}
	}
}

func TestMoveLinkScopes(t *testing.T) {
	a := moveLinks(t)
	exp, imp, read := "pcx_bob_export", "pcx_bob_import", "pcx_bob_read"
	a.checkMoveScopes([]moveCase{
		{exp, "GET", core.PortablePath, 200},
		{exp, "GET", "/api/v1/export.json", 200},
		{exp, "GET", "/api/v1/export.csv", 200},
		{exp, "GET", "/api/v1/stations/export.m3u", 200},
		{exp, "POST", "/api/v1/import", 403},
		{exp, "POST", "/api/v1/stations/import", 403},
		{exp, "GET", "/api/v1/stations", 403},
		{exp, "GET", "/api/v1/settings", 403},
		{exp, "GET", "/api/v1/backup", 403},
		{imp, "GET", core.PortablePath, 200},
		{imp, "GET", "/api/v1/export.json", 403},
		{imp, "POST", "/api/v1/stations", 403},
		{imp, "POST", "/api/v1/stations/import/navidrome", 403},
		{imp, "PUT", "/api/v1/settings", 403},
		{read, "GET", core.PortablePath, 403},
		{read, "GET", "/api/v1/export.json", 403},
	})
	_, body := a.moveRaw(exp, "GET", core.PortablePath, "", nil)
	var p core.Portable
	if err := json.Unmarshal(body, &p); err != nil || len(p.Exports) != 3 || len(p.Imports) != 2 || p.Exports[0].Kind != core.KindPocket {
		t.Fatalf("portable: %s", body)
	}
}

// Move brings Alice's radio to Bob: each import adds once, and nothing of
// one person shows up for the other.
func TestMoveImportsAddOnce(t *testing.T) {
	a := moveLinks(t, func(c *config.Config) { c.DemoData = true })
	code, export := a.moveRaw("pcx_alice_export", "GET", "/api/v1/export.json", "", nil)
	var exp exportDoc
	if err := json.Unmarshal(export, &exp); code != 200 || err != nil || len(exp.Stations) < 10 || len(exp.Listens) == 0 {
		t.Fatalf("alice's export: %d %v", code, err)
	}
	if _, bob := a.moveRaw("pcx_bob_export", "GET", "/api/v1/export.json", "", nil); strings.Contains(string(bob), exp.Stations[0].URL) {
		t.Fatalf("bob's export holds alice's stations: %s", bob)
	}
	first := a.moveImport("pcx_bob_import", "/api/v1/import", "", "application/json", export)
	if int(num(first, "stations")) != len(exp.Stations) || int(num(first, "listens")) != len(exp.Listens) {
		t.Fatalf("first import: %v", first)
	}
	second := a.moveImport("pcx_bob_import", "/api/v1/import", "", "application/json", export)
	for _, k := range []string{"stations", "channels", "listens", "heard"} {
		if num(second, k) != 0 {
			t.Fatalf("second import added %s: %v", k, second)
		}
	}
	m3u := []byte("#EXTM3U\n#EXTINF:-1,News 24\nhttps://news.example/live.mp3\n")
	if m := a.moveImport("pcx_bob_import", "/api/v1/stations/import", "", "audio/x-mpegurl", m3u); num(m, "added") != 1 {
		t.Fatalf("m3u import: %v", m)
	}
	if m := a.moveImport("pcx_bob_import", "/api/v1/stations/import", "", "audio/x-mpegurl", m3u); num(m, "added") != 0 {
		t.Fatalf("m3u import twice: %v", m)
	}
	if l := a.list(a.alice, "/api/v1/stations?view=all"); len(l) != len(exp.Stations) {
		t.Fatalf("alice's stations changed: %d", len(l))
	}
	if l := a.list(a.bob, "/api/v1/stations?view=all"); len(l) != len(exp.Stations)+1 {
		t.Fatalf("bob's stations: %d", len(l))
	}
}
