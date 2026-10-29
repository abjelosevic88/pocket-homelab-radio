package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// ---- channels on air ----
//
// A channel plays like a real station: while anyone listens, one
// broadcaster picks the songs, reads each from Navidrome as MP3 and sends
// the frames in real time to every listener, so the phone and the car hear
// the same song at the same moment. It stays a few seconds ahead of the
// clock (players fill their buffer from that, and newcomers get the last
// seconds at once), goes off air 20 seconds after the last listener leaves
// and starts fresh on the next tune-in.

const (
	bcLead   = 3 * time.Second  // how far ahead of real time the frames go out
	bcBurst  = 6 * time.Second  // what a new listener gets at once
	bcLinger = 20 * time.Second // on air after the last listener left
	icyEvery = 16000            // bytes of sound between two ICY titles
)

type broadcaster struct {
	s   *Server
	uid int64
	id  int64

	mu        sync.Mutex
	name      string
	listeners map[*bcListener]bool
	burst     [][]byte
	burstDur  []time.Duration
	cur       navSong
	curStart  time.Time
	errText   string
	finished  bool
	stop      context.CancelFunc
	idle      *time.Timer
}

type bcListener struct {
	ch   chan []byte
	live *liveStream
	gone bool
}

// tuneIn joins a channel's broadcast, starting it if it is off air.
func (s *Server) tuneIn(uid int64, c *store.Channel) (*broadcaster, *bcListener) {
	s.bcMu.Lock()
	defer s.bcMu.Unlock()
	b := s.bcasts[c.ID]
	if b == nil || b.isFinished() {
		ctx, cancel := context.WithCancel(context.Background())
		b = &broadcaster{s: s, uid: uid, id: c.ID, name: c.Name, listeners: map[*bcListener]bool{}, stop: cancel}
		s.bcasts[c.ID] = b
		go b.run(ctx)
	}
	return b, b.add()
}

func (b *broadcaster) isFinished() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.finished
}

func (b *broadcaster) add() *bcListener {
	b.mu.Lock()
	defer b.mu.Unlock()
	l := &bcListener{ch: make(chan []byte, 2048)}
	for _, f := range b.burst {
		l.ch <- f
	}
	b.listeners[l] = true
	if b.idle != nil {
		b.idle.Stop()
		b.idle = nil
	}
	return l
}

func (b *broadcaster) remove(l *bcListener) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.listeners[l] {
		delete(b.listeners, l)
		if !l.gone {
			l.gone = true
			close(l.ch)
		}
	}
	if len(b.listeners) == 0 && !b.finished && b.idle == nil {
		b.idle = time.AfterFunc(bcLinger, func() {
			b.mu.Lock()
			empty := len(b.listeners) == 0
			if empty {
				b.finished = true // a new tune-in starts a fresh broadcast
			}
			b.mu.Unlock()
			if empty {
				b.stop()
			}
		})
	}
}

// send hands a frame to every listener; one that cannot keep up (its
// buffer full for ~50 s of sound) is let go.
func (b *broadcaster) send(frame []byte, dur time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.burst = append(b.burst, frame)
	b.burstDur = append(b.burstDur, dur)
	var total time.Duration
	for _, d := range b.burstDur {
		total += d
	}
	for total > bcBurst && len(b.burst) > 1 {
		total -= b.burstDur[0]
		b.burst, b.burstDur = b.burst[1:], b.burstDur[1:]
	}
	for l := range b.listeners {
		select {
		case l.ch <- frame:
		default:
			delete(b.listeners, l)
			l.gone = true
			close(l.ch)
		}
	}
}

func (b *broadcaster) setSong(ctx context.Context, x navSong) {
	b.mu.Lock()
	b.cur, b.curStart, b.errText = x, time.Now().Add(bcLead), ""
	var lives []*liveStream
	for l := range b.listeners {
		if l.live != nil {
			lives = append(lives, l.live)
		}
	}
	b.mu.Unlock()
	for _, lv := range lives {
		b.s.liveTitle(ctx, lv, songHeard(x))
	}
}

func songHeard(x navSong) store.Heard {
	return store.Heard{Title: songTitle(x), Artist: x.Artist, Song: x.Title, SongID: x.ID}
}

func songTitle(x navSong) string {
	if x.Artist == "" {
		return x.Title
	}
	return x.Artist + " - " + x.Title
}

func (b *broadcaster) setErr(msg string) {
	b.mu.Lock()
	b.errText = msg
	b.mu.Unlock()
}

// state is what /channels/{id}/now shows.
func (b *broadcaster) state() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]any{"on_air": !b.finished, "listeners": len(b.listeners), "error": b.errText}
	if b.cur.ID != "" {
		out["song"] = map[string]any{"id": b.cur.ID, "title": b.cur.Title, "artist": b.cur.Artist, "album": b.cur.Album, "year": b.cur.Year,
			"duration": b.cur.Duration, "cover": firstNonEmpty(b.cur.CoverArt, b.cur.AlbumID)}
		out["started_at"] = b.curStart.UTC().Format(time.RFC3339)
	}
	return out
}

func (b *broadcaster) title() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cur.ID == "" {
		return b.name
	}
	return songTitle(b.cur)
}

// run is the broadcast: pick, stream, repeat — until nobody listens, the
// channel is deleted or the library fails three times running.
func (b *broadcaster) run(ctx context.Context) {
	s := b.s
	defer func() {
		b.mu.Lock()
		b.finished = true
		for l := range b.listeners {
			if !l.gone {
				l.gone = true
				close(l.ch)
			}
		}
		b.listeners = map[*bcListener]bool{}
		b.mu.Unlock()
		s.bcMu.Lock()
		if s.bcasts[b.id] == b {
			delete(s.bcasts, b.id)
		}
		s.bcMu.Unlock()
	}()
	clock := time.Now()
	fails := 0
	failed := func(msg string) bool {
		b.setErr(msg)
		s.log.Warn("channel: cannot play", "channel", b.id, "err", msg)
		if fails++; fails >= 3 {
			return true
		}
		select {
		case <-ctx.Done():
			return true
		case <-time.After(3 * time.Second):
		}
		return false
	}
	for ctx.Err() == nil {
		c, err := s.store.GetChannel(ctx, b.uid, b.id)
		if err != nil {
			return
		}
		b.mu.Lock()
		b.name = c.Name
		b.mu.Unlock()
		nav, err := s.navidromeFor(ctx, b.uid)
		if err != nil {
			if failed(err.Error()) {
				return
			}
			continue
		}
		song, err := s.pickSong(ctx, nav, c)
		if err != nil {
			if failed(err.Error()) {
				return
			}
			continue
		}
		res, err := nav.stream(ctx, song.ID, c.Bitrate)
		if err != nil {
			if failed(err.Error()) {
				return
			}
			continue
		}
		b.setSong(ctx, song)
		_ = s.store.ChannelPlayed(ctx, b.uid, b.id, song.ID, song.Artist)
		mr := newMP3Reader(res.Body)
		frames := 0
		for ctx.Err() == nil {
			frame, dur, err := mr.next()
			if err != nil {
				break
			}
			frames++
			if ahead := time.Until(clock); ahead > bcLead+200*time.Millisecond {
				select {
				case <-ctx.Done():
				case <-time.After(ahead - bcLead):
				}
			} else if ahead < -5*time.Second {
				clock = time.Now() // the library was slow: carry on from now
			}
			clock = clock.Add(dur)
			b.send(frame, dur)
		}
		res.Body.Close()
		if frames == 0 {
			if failed("Navidrome sent a song that is not MP3 (is transcoding to MP3 switched on?)") {
				return
			}
			continue
		}
		fails = 0
	}
}

// ---- listening to a channel ----

// handleChannelStream is the web player's stream: GET /stream/channel/{id}?sid=….
func (s *Server) handleChannelStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	c, err := s.store.GetChannel(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "channel")
		return
	}
	s.serveChannel(w, r, uid, c, clientOf(r))
}

// handleListen is a channel's address for other players and radio apps:
// GET /listen/{token}.mp3 (the token stands in for signing in), or
// /listen/{token}.m3u for a playlist file that points at it.
func (s *Server) handleListen(w http.ResponseWriter, r *http.Request) {
	name := chiParam(r, "file")
	tok, ext := name, ""
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		tok, ext = name[:i], strings.ToLower(name[i+1:])
	}
	c, err := s.store.ChannelByToken(r.Context(), tok)
	if err != nil {
		http.Error(w, "no such station", http.StatusNotFound)
		return
	}
	switch ext {
	case "m3u", "m3u8", "pls":
		addr := s.listenURL(r, c)
		if ext == "pls" {
			setAttachment(w, safeFile(c.Name)+".pls", "audio/x-scpls")
			_, _ = w.Write([]byte("[playlist]\nNumberOfEntries=1\nFile1=" + addr + "\nTitle1=" + c.Name + "\nLength1=-1\nVersion=2\n"))
			return
		}
		setAttachment(w, safeFile(c.Name)+".m3u", "audio/x-mpegurl")
		_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:-1," + c.Name + "\n" + addr + "\n"))
		return
	case "", "mp3":
		s.serveChannel(w, r, c.UserID, c, "app")
		return
	}
	http.Error(w, "no such station", http.StatusNotFound)
}

func (s *Server) listenURL(r *http.Request, c *store.Channel) string {
	base := strings.TrimRight(s.cfg.BaseURL, "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/listen/" + c.ListenToken + ".mp3"
}

func safeFile(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == '"' || r == '/' || r == '\\' || r < 32 {
			continue
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "station"
	}
	return b.String()
}

func (s *Server) serveChannel(w http.ResponseWriter, r *http.Request, uid int64, c *store.Channel, client string) {
	ctx := r.Context()
	if _, err := s.navidromeFor(ctx, uid); err != nil {
		writeErr(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	b, l := s.tuneIn(uid, c)
	defer b.remove(l)
	live := s.liveStart(ctx, uid, "channel", c.ID, c.Name, r.URL.Query().Get("sid"), client)
	defer s.liveEnd(ctx, live)
	b.mu.Lock()
	l.live = live
	cur := b.cur
	b.mu.Unlock()
	if cur.ID != "" {
		s.liveTitle(ctx, live, songHeard(cur))
	}
	icy := r.Header.Get("Icy-MetaData") == "1"
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("icy-name", c.Name)
	w.Header().Set("icy-br", strconv.Itoa(c.Bitrate))
	if c.Description != "" {
		w.Header().Set("icy-description", strings.ReplaceAll(c.Description, "\n", " "))
	}
	if icy {
		w.Header().Set("icy-metaint", strconv.Itoa(icyEvery))
	}
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	toMeta := icyEvery
	sentTitle := ""
	touch := time.NewTicker(15 * time.Second)
	defer touch.Stop()
	write := func(frame []byte) error {
		if !icy {
			_, err := w.Write(frame)
			return err
		}
		for len(frame) > 0 {
			n := min(len(frame), toMeta)
			if _, err := w.Write(frame[:n]); err != nil {
				return err
			}
			frame, toMeta = frame[n:], toMeta-n
			if toMeta == 0 {
				t := b.title()
				block := []byte{0}
				if t != sentTitle {
					block = icyBlock(t)
					sentTitle = t
				}
				if _, err := w.Write(block); err != nil {
					return err
				}
				toMeta = icyEvery
			}
		}
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-touch.C:
			s.liveTouch(ctx, live)
		case frame, ok := <-l.ch:
			if !ok {
				return
			}
			if err := write(frame); err != nil {
				return
			}
			if len(l.ch) == 0 {
				if err := rc.Flush(); err != nil {
					return
				}
			}
		}
	}
}

// icyBlock is one ICY metadata block: a length byte (×16) and the padded text.
func icyBlock(title string) []byte {
	t := strings.ReplaceAll(title, "'", "’")
	meta := []byte("StreamTitle='" + t + "';")
	if len(meta) > 255*16 {
		meta = meta[:255*16]
	}
	n := (len(meta) + 15) / 16
	out := make([]byte, 1+n*16)
	out[0] = byte(n)
	copy(out[1:], meta)
	return out
}

// handleChannelNow is what a channel plays now (or that it is off air).
func (s *Server) handleChannelNow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	c, err := s.store.GetChannel(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "channel")
		return
	}
	s.bcMu.Lock()
	b := s.bcasts[c.ID]
	s.bcMu.Unlock()
	out := map[string]any{"on_air": false, "listeners": 0}
	if b != nil {
		out = b.state()
	}
	recent, _ := s.store.ListHeard(ctx, uid, store.HeardFilter{ChannelID: c.ID, Limit: 8})
	out["recent"] = recent
	writeJSON(w, http.StatusOK, out)
}

var errNoSongs = errors.New("no songs in your library match this channel's rules")
