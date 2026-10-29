package server

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// ---- channels: stations of your own ----

type channelInput struct {
	Name        *string             `json:"name"`
	Description *string             `json:"description"`
	Rules       *store.ChannelRules `json:"rules"`
	Color       *string             `json:"color"`
	Bitrate     *int                `json:"bitrate"`
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

var bitrates = map[int]bool{96: true, 128: true, 160: true, 192: true, 256: true, 320: true}

func (in channelInput) apply(c *store.Channel) string {
	if in.Name != nil {
		c.Name = clip(*in.Name, 80)
	}
	if in.Description != nil {
		c.Description = clip(*in.Description, 500)
	}
	if in.Rules != nil {
		c.Rules = *in.Rules
		for _, l := range []*[]string{&c.Rules.Genres, &c.Rules.ExcludeGenres, &c.Rules.Artists, &c.Rules.ExcludeArtists, &c.Rules.Playlists} {
			for i := range *l {
				(*l)[i] = clip((*l)[i], 120)
			}
			if len(*l) > 50 {
				*l = (*l)[:50]
			}
		}
		c.Rules.Clean()
	}
	if in.Color != nil {
		c.Color = strings.TrimSpace(*in.Color)
		if c.Color != "" && !hexColor.MatchString(c.Color) {
			return "the colour looks like #2f7d6d"
		}
	}
	if in.Bitrate != nil {
		if !bitrates[*in.Bitrate] {
			return "the sound quality is 96, 128, 160, 192, 256 or 320 kbit/s"
		}
		c.Bitrate = *in.Bitrate
	}
	if c.Name == "" {
		return "the channel needs a name"
	}
	return ""
}

// channelView adds the listen addresses and what plays now.
func (s *Server) channelView(r *http.Request, c *store.Channel) map[string]any {
	s.bcMu.Lock()
	b := s.bcasts[c.ID]
	s.bcMu.Unlock()
	onAir := false
	nowTitle := ""
	if b != nil {
		st := b.state()
		onAir, _ = st["on_air"].(bool)
		nowTitle = b.title()
		if nowTitle == c.Name {
			nowTitle = ""
		}
	}
	base := s.listenURL(r, c)
	return map[string]any{"id": c.ID, "name": c.Name, "description": c.Description, "rules": c.Rules, "color": c.Color, "bitrate": c.Bitrate,
		"last_played_at": c.LastPlayedAt, "created_at": c.CreatedAt, "updated_at": c.UpdatedAt,
		"listen_url": base, "playlist_url": strings.TrimSuffix(base, ".mp3") + ".m3u", "on_air": onAir, "now": nowTitle}
}

func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := s.store.ListChannels(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "channels")
		return
	}
	out := make([]map[string]any, 0, len(list))
	for i := range list {
		out = append(out, s.channelView(r, &list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.store.GetChannel(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "channel")
		return
	}
	writeJSON(w, http.StatusOK, s.channelView(r, c))
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	c := store.Channel{Bitrate: st.ChannelBitrate, Rules: store.ChannelRules{AvoidHours: 24, ArtistGap: 3}}
	if !bitrates[c.Bitrate] {
		c.Bitrate = 192
	}
	var in channelInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := in.apply(&c); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.CreateChannel(ctx, uid, &c); err != nil {
		s.fail(w, err, "add channel")
		return
	}
	writeJSON(w, http.StatusCreated, s.channelView(r, &c))
}

func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	c, err := s.store.GetChannel(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "channel")
		return
	}
	var in channelInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := in.apply(c); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.UpdateChannel(ctx, uid, c); err != nil {
		s.fail(w, err, "save channel")
		return
	}
	s.pools.mu.Lock()
	delete(s.pools.m, c.ID)
	s.pools.mu.Unlock()
	writeJSON(w, http.StatusOK, s.channelView(r, c))
}

func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	id := idParam(r, "id")
	if err := s.store.DeleteChannel(ctx, uid, id); err != nil {
		s.fail(w, err, "delete channel")
		return
	}
	s.bcMu.Lock()
	b := s.bcasts[id]
	s.bcMu.Unlock()
	if b != nil && b.uid == uid {
		b.stop()
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleChannelNewAddress replaces a channel's listen address; players
// with the old one stop.
func (s *Server) handleChannelNewAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	id := idParam(r, "id")
	if _, err := s.store.NewChannelToken(ctx, uid, id); err != nil {
		s.fail(w, err, "channel")
		return
	}
	s.bcMu.Lock()
	b := s.bcasts[id]
	s.bcMu.Unlock()
	if b != nil && b.uid == uid {
		b.stop()
	}
	c, err := s.store.GetChannel(ctx, uid, id)
	if err != nil {
		s.fail(w, err, "channel")
		return
	}
	writeJSON(w, http.StatusOK, s.channelView(r, c))
}
