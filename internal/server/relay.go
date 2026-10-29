package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// ---- the station relay ----
//
// Stations play through the server: the page is https, many streams are
// plain http (a browser refuses those), and only the server can read the
// song titles a station puts inside its stream (ICY metadata). The relay
// asks for the titles, takes them out of the audio and passes the sound on
// as it comes, so the player gets a clean stream and the titles go to
// /api/v1/now and the heard list.

// handleStationStream plays one of the person's stations: GET
// /stream/station/{id}?sid=….
func (s *Server) handleStationStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	st, err := s.store.GetStation(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "station")
		return
	}
	if st.HLS {
		writeErr(w, http.StatusConflict, "this station streams as HLS and plays straight in the browser")
		return
	}
	res, err := s.openStation(ctx, uid, st)
	if err != nil {
		code := http.StatusBadGateway
		if isLocalErr(err) {
			code = http.StatusForbidden
		}
		writeErr(w, code, err.Error())
		return
	}
	defer res.Body.Close()
	metaint, _ := strconv.Atoi(res.Header.Get("icy-metaint"))
	ct := res.Header.Get("Content-Type")
	if ct == "" || ct == "application/octet-stream" {
		ct = "audio/mpeg"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	l := s.liveStart(ctx, uid, "station", st.ID, st.Name, r.URL.Query().Get("sid"), clientOf(r))
	defer s.liveEnd(ctx, l)
	_ = s.store.MarkStationPlayed(ctx, uid, st.ID)
	s.directoryClick(st.DirectoryID)

	onTitle := func(title string) {
		if !usefulTitle(title, st.Name) {
			return
		}
		artist, song := splitTitle(title)
		s.liveTitle(ctx, l, store.Heard{Title: title, Artist: artist, Song: song})
	}
	_ = relayICY(ctx, w, res.Body, metaint, onTitle, func() { s.liveTouch(ctx, l) })
}

// clientOf tells the web player from other players.
func clientOf(r *http.Request) string {
	if r.URL.Query().Get("sid") != "" {
		return "web"
	}
	return "app"
}

// openStation connects to the stream, following a playlist file or a
// changed address once (and remembering the new one).
func (s *Server) openStation(ctx context.Context, uid int64, st *store.Station) (*http.Response, error) {
	c := s.outClient(0)
	open := func(u string) (*http.Response, error) {
		res, err := s.outGet(ctx, c, u, map[string]string{"Icy-MetaData": "1", "Accept": "*/*"})
		if err != nil {
			return nil, err
		}
		if res.StatusCode >= 400 {
			res.Body.Close()
			return nil, errors.New("the station answered HTTP " + strconv.Itoa(res.StatusCode))
		}
		ct := res.Header.Get("Content-Type")
		if looksLikePlaylist(ct, res.Request.URL.Path, nil) || !audioType(ct) {
			res.Body.Close()
			return nil, errNeedsProbe
		}
		return res, nil
	}
	res, err := open(st.StreamURL())
	if err == nil {
		return res, nil
	}
	if isLocalErr(err) {
		return nil, err
	}
	p, perr := s.probeStream(ctx, st.URL)
	if perr != nil {
		if errors.Is(err, errNeedsProbe) {
			return nil, perr
		}
		return nil, err
	}
	if p.HLS {
		st.HLS = true
		st.URLResolved = p.URL
		_ = s.store.UpdateStation(context.WithoutCancel(ctx), uid, st)
		return nil, errors.New("this station streams as HLS; reload to play it")
	}
	if p.URL != st.URLResolved {
		_ = s.store.SetStationResolved(context.WithoutCancel(ctx), uid, st.ID, p.URL, p.Codec, p.Bitrate)
	}
	return open(p.URL)
}

var errNeedsProbe = errors.New("needs a probe")

// relayICY copies a stream to w, taking the ICY metadata blocks out (every
// metaint bytes) and handing each StreamTitle to onTitle. tick runs every
// 15 seconds. It returns when either side closes.
func relayICY(ctx context.Context, w http.ResponseWriter, body io.Reader, metaint int, onTitle func(string), tick func()) error {
	rc := http.NewResponseController(w)
	buf := make([]byte, 16<<10)
	last := time.Now()
	write := func(b []byte) error {
		if _, err := w.Write(b); err != nil {
			return err
		}
		_ = rc.Flush()
		if time.Since(last) >= 15*time.Second {
			last = time.Now()
			tick()
		}
		return nil
	}
	if metaint <= 0 || metaint > 1<<20 {
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			n, err := body.Read(buf)
			if n > 0 {
				if werr := write(buf[:n]); werr != nil {
					return werr
				}
			}
			if err != nil {
				return err
			}
		}
	}
	left := metaint
	one := make([]byte, 1)
	meta := make([]byte, 255*16)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := body.Read(buf[:min(left, len(buf))])
		if n > 0 {
			if werr := write(buf[:n]); werr != nil {
				return werr
			}
			left -= n
		}
		if err != nil {
			return err
		}
		if left > 0 {
			continue
		}
		if _, err := io.ReadFull(body, one); err != nil {
			return err
		}
		if size := int(one[0]) * 16; size > 0 {
			if _, err := io.ReadFull(body, meta[:size]); err != nil {
				return err
			}
			if t, ok := streamTitle(meta[:size]); ok {
				onTitle(t)
			}
		}
		left = metaint
	}
}

// streamTitle reads StreamTitle='…'; from an ICY metadata block.
func streamTitle(b []byte) (string, bool) {
	b = bytes.TrimRight(b, "\x00")
	i := bytes.Index(b, []byte("StreamTitle='"))
	if i < 0 {
		return "", false
	}
	rest := b[i+len("StreamTitle='"):]
	end := bytes.Index(rest, []byte("';"))
	if end < 0 {
		end = bytes.LastIndexByte(rest, '\'')
		if end < 0 {
			end = len(rest)
		}
	}
	t := strings.TrimSpace(toUTF8(rest[:end]))
	return t, true
}

// toUTF8 keeps UTF-8 titles and reads anything else as Latin-1 (what old
// Shoutcast servers send).
func toUTF8(b []byte) string {
	if s := string(b); strings.ToValidUTF8(s, "�") == s {
		return s
	}
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}
