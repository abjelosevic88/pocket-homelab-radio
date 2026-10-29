package server

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// ---- .pls / .m3u files ----
//
// Many stations publish a small playlist file rather than the stream
// itself, and other radio apps export their favourites the same way.

// playlistEntry is one stream in a playlist file.
type playlistEntry struct {
	Name string
	URL  string
	Logo string
	Tags string
}

var plsFile = regexp.MustCompile(`(?i)^file(\d+)\s*=\s*(.+)$`)
var plsTitle = regexp.MustCompile(`(?i)^title(\d+)\s*=\s*(.*)$`)
var m3uAttr = regexp.MustCompile(`([a-zA-Z-]+)="([^"]*)"`)

// isHLS reports whether a playlist is an HLS media playlist (played as
// HLS, not unwrapped).
func isHLS(b []byte) bool {
	return bytes.Contains(b, []byte("#EXT-X-TARGETDURATION")) || bytes.Contains(b, []byte("#EXT-X-STREAM-INF")) || bytes.Contains(b, []byte("#EXT-X-MEDIA-SEQUENCE"))
}

// parsePlaylist reads .pls, .m3u / .m3u8 (non-HLS) or a plain list of URLs.
func parsePlaylist(b []byte) []playlistEntry {
	text := strings.TrimPrefix(string(b), "\ufeff")
	if strings.Contains(strings.ToLower(text[:min(len(text), 200)]), "[playlist]") {
		files := map[int]*playlistEntry{}
		sc := bufio.NewScanner(strings.NewReader(text))
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if m := plsFile.FindStringSubmatch(line); m != nil {
				n, _ := strconv.Atoi(m[1])
				if files[n] == nil {
					files[n] = &playlistEntry{}
				}
				files[n].URL = strings.TrimSpace(m[2])
			} else if m := plsTitle.FindStringSubmatch(line); m != nil {
				n, _ := strconv.Atoi(m[1])
				if files[n] == nil {
					files[n] = &playlistEntry{}
				}
				files[n].Name = strings.TrimSpace(m[2])
			}
		}
		keys := make([]int, 0, len(files))
		for k := range files {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		var out []playlistEntry
		for _, k := range keys {
			if e := files[k]; validStreamURL(e.URL) {
				out = append(out, *e)
			}
		}
		return out
	}
	var out []playlistEntry
	var pending playlistEntry
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
		case strings.HasPrefix(line, "#EXTINF"):
			pending = playlistEntry{}
			head, name, _ := strings.Cut(line, ",")
			// attributes may contain commas inside quotes: take the name after the last quote
			if q := strings.LastIndex(line, `"`); q > 0 && q > len(head) {
				name = strings.TrimPrefix(strings.TrimSpace(line[q+1:]), ",")
			}
			pending.Name = strings.TrimSpace(name)
			for _, m := range m3uAttr.FindAllStringSubmatch(line, -1) {
				switch strings.ToLower(m[1]) {
				case "tvg-logo", "logo":
					pending.Logo = m[2]
				case "group-title":
					pending.Tags = m[2]
				}
			}
		case strings.HasPrefix(line, "#"):
		default:
			if validStreamURL(line) {
				pending.URL = line
				out = append(out, pending)
			}
			pending = playlistEntry{}
		}
	}
	return out
}

func validStreamURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// streamProbe is what a station address turned out to be.
type streamProbe struct {
	URL         string // the stream itself, after playlist files
	HLS         bool
	ContentType string
	Codec       string
	Bitrate     int
	Name        string // icy-name
	Genre       string // icy-genre
	Homepage    string // icy-url
}

var playlistTypes = []string{"audio/x-scpls", "audio/scpls", "audio/mpegurl", "audio/x-mpegurl", "application/x-mpegurl", "application/vnd.apple.mpegurl",
	"application/pls+xml", "audio/x-pn-realaudio", "text/plain", "text/html", "application/octet-stream"}

func looksLikePlaylist(ct, path string, head []byte) bool {
	ct = strings.ToLower(ct)
	p := strings.ToLower(path)
	if strings.HasSuffix(p, ".pls") || strings.HasSuffix(p, ".m3u") || strings.HasSuffix(p, ".m3u8") {
		return true
	}
	t := bytes.TrimSpace(bytes.TrimPrefix(head, []byte("\ufeff")))
	if bytes.HasPrefix(bytes.ToLower(t), []byte("[playlist]")) || bytes.HasPrefix(t, []byte("#EXTM3U")) {
		return true
	}
	for _, x := range playlistTypes[:6] {
		if strings.HasPrefix(ct, x) {
			return true
		}
	}
	return false
}

func audioType(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.HasPrefix(ct, "audio/") || strings.HasPrefix(ct, "application/ogg") || strings.HasPrefix(ct, "video/mp2t") ||
		strings.HasPrefix(ct, "application/aacp") || strings.HasPrefix(ct, "video/ogg") || ct == "application/octet-stream" || ct == ""
}

func codecOf(ct string) string {
	ct = strings.ToLower(ct)
	switch {
	case strings.Contains(ct, "mpeg") && !strings.Contains(ct, "url"):
		return "MP3"
	case strings.Contains(ct, "aac"):
		return "AAC"
	case strings.Contains(ct, "ogg"), strings.Contains(ct, "opus"), strings.Contains(ct, "vorbis"):
		return "OGG"
	case strings.Contains(ct, "flac"):
		return "FLAC"
	}
	return ""
}

// probeStream opens an address, unwraps up to three playlist files and
// reports what plays. It reads only the first bytes of a stream.
func (s *Server) probeStream(ctx context.Context, raw string) (*streamProbe, error) {
	c := s.outClient(0)
	cur := strings.TrimSpace(raw)
	for hop := 0; hop < 4; hop++ {
		if !validStreamURL(cur) {
			return nil, errors.New("the address must start with http:// or https://")
		}
		res, err := s.outGet(ctx, c, cur, map[string]string{"Icy-MetaData": "1", "Accept": "*/*"})
		if err != nil {
			return nil, err
		}
		final := res.Request.URL.String()
		ct := res.Header.Get("Content-Type")
		if res.StatusCode >= 400 {
			res.Body.Close()
			return nil, fmt.Errorf("the station answered HTTP %d", res.StatusCode)
		}
		head := make([]byte, 512)
		n, _ := io.ReadFull(res.Body, head)
		head = head[:n]
		if looksLikePlaylist(ct, res.Request.URL.Path, head) {
			rest, _ := io.ReadAll(io.LimitReader(res.Body, 256<<10))
			res.Body.Close()
			body := append(head, rest...)
			if isHLS(body) {
				return &streamProbe{URL: final, HLS: true, ContentType: ct, Codec: "HLS"}, nil
			}
			entries := parsePlaylist(body)
			if len(entries) == 0 {
				return nil, errors.New("this playlist file has no stream in it")
			}
			next, err := res.Request.URL.Parse(entries[0].URL)
			if err != nil {
				return nil, err
			}
			cur = next.String()
			continue
		}
		res.Body.Close()
		if !audioType(ct) {
			return nil, fmt.Errorf("this is not a radio stream (%s)", strings.SplitN(ct, ";", 2)[0])
		}
		p := &streamProbe{URL: final, ContentType: ct, Codec: codecOf(ct), Name: strings.TrimSpace(res.Header.Get("icy-name")),
			Genre: strings.TrimSpace(res.Header.Get("icy-genre")), Homepage: strings.TrimSpace(res.Header.Get("icy-url"))}
		if br, err := strconv.Atoi(strings.SplitN(res.Header.Get("icy-br"), ",", 2)[0]); err == nil {
			p.Bitrate = br
		}
		return p, nil
	}
	return nil, errors.New("too many playlist files inside each other")
}

// exportM3U writes stations as an extended M3U playlist other radio apps
// (VLC, foobar2000, most phone radio apps) can import.
func exportM3U(w io.Writer, list []store.Station) {
	fmt.Fprintln(w, "#EXTM3U")
	for _, st := range list {
		attrs := ""
		if st.LogoURL != "" {
			attrs += fmt.Sprintf(` tvg-logo="%s"`, strings.ReplaceAll(st.LogoURL, `"`, ""))
		}
		if st.Tags != "" {
			attrs += fmt.Sprintf(` group-title="%s"`, strings.ReplaceAll(st.Tags, `"`, ""))
		}
		fmt.Fprintf(w, "#EXTINF:-1%s,%s\n%s\n", attrs, strings.ReplaceAll(st.Name, "\n", " "), st.URL)
	}
}

func setAttachment(w http.ResponseWriter, name, ct string) {
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
}
