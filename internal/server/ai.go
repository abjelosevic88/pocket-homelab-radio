package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-radio/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// aiFor is the person's AI: Pocket AI through Core, their own
// OpenAI-compatible key, or nil with why.
func (s *Server) aiFor(ctx context.Context, uid int64, st store.Settings) (*ai.Client, string, string) {
	email := ""
	if u, err := s.store.GetUser(ctx, uid); err == nil {
		email = u.Email
	}
	pocket, status := s.core.SelectAI(ctx, email, st.AIProvider, st.OpenAIKey != "")
	if pocket != nil {
		if !status.Ready {
			return nil, "", "Pocket AI: " + status.Detail
		}
		c := ai.New("", "", "", 150*time.Second)
		c.Pocket = pocket.WithModel(st.AIPocketModel)
		return c, "pocket", ""
	}
	if st.OpenAIKey == "" {
		return nil, "", status.Detail
	}
	return ai.New(st.OpenAIBaseURL, st.OpenAIKey, st.OpenAIModel, 150*time.Second), "openai", ""
}

func (s *Server) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	cl, used, why := s.aiFor(ctx, actorID(ctx), st)
	writeJSON(w, http.StatusOK, map[string]any{"ready": cl != nil, "provider": used, "detail": why})
}

func (s *Server) handleAIModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, status := s.core.SelectAI(ctx, userFrom(ctx).Email, "pocket", false)
	if c == nil || !status.Ready {
		writeErr(w, http.StatusServiceUnavailable, status.Detail)
		return
	}
	models, err := c.Models(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func validAIProvider(v string) bool { return core.ValidAIProvider(v) }

func validPocketModel(v string) bool { return core.ValidInferenceModel(v) }

// ---- a channel in your own words ----

// handleDescribeChannel turns "calm jazz and soul from the sixties for
// Sunday mornings" into channel rules, with the genres the person's library
// actually has. It only drafts: nothing is saved. POST
// /api/v1/channels/describe {text}.
func (s *Server) handleDescribeChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		Text string `json:"text"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Text) == "" {
		writeErr(w, http.StatusBadRequest, "say what the channel should play")
		return
	}
	st, _ := s.store.GetSettings(ctx, uid)
	cl, used, why := s.aiFor(ctx, uid, st)
	if cl == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": firstNonEmpty(why, "AI is not set up (Settings → AI)"), "not_configured": true})
		return
	}
	nav, err := s.navidromeFor(ctx, uid)
	if err != nil {
		s.libraryError(w, err)
		return
	}
	gs, err := nav.genres(ctx)
	if err != nil {
		s.libraryError(w, err)
		return
	}
	sortGenres(gs)
	if len(gs) > 250 {
		gs = gs[:250]
	}
	var list strings.Builder
	known := map[string]string{}
	for _, g := range gs {
		fmt.Fprintf(&list, "%s (%d)\n", g.Name, g.SongCount)
		known[strings.ToLower(g.Name)] = g.Name
	}
	system := "You set up a radio channel that plays from a person's own music library. Answer with JSON only: " +
		`{"name": short station name (2-4 words), "description": one plain sentence, "genres": [genres to play, ONLY names from the list, exactly as written], ` +
		`"exclude_genres": [genres from the list to keep out], "artists": [artists the person named, if any], "year_from": number or 0, "year_to": number or 0}. ` +
		"Prefer several matching genres over one. Leave artists empty unless the person named artists. Use years only when the person asked for an era."
	prompt := "What the channel should play: " + clip(in.Text, 600) + "\n\nGenres in the library (songs):\n" + list.String()
	var out struct {
		Name          string   `json:"name"`
		Description   string   `json:"description"`
		Genres        []string `json:"genres"`
		ExcludeGenres []string `json:"exclude_genres"`
		Artists       []string `json:"artists"`
		YearFrom      int      `json:"year_from"`
		YearTo        int      `json:"year_to"`
	}
	if err := cl.JSON(ctx, system, prompt, &out); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "AI: " + err.Error()})
		return
	}
	keep := func(in []string) []string {
		res := []string{}
		for _, g := range in {
			if name, ok := known[strings.ToLower(strings.TrimSpace(g))]; ok {
				res = append(res, name)
			}
		}
		return res
	}
	rules := store.ChannelRules{Genres: keep(out.Genres), ExcludeGenres: keep(out.ExcludeGenres), YearFrom: out.YearFrom, YearTo: out.YearTo,
		AvoidHours: 24, ArtistGap: 3}
	// artists only when the library has them
	for _, a := range out.Artists {
		hits, err := nav.searchArtists(ctx, a, 5)
		if err != nil {
			continue
		}
		for _, h := range hits {
			if strings.EqualFold(h.Name, strings.TrimSpace(a)) {
				rules.Artists = append(rules.Artists, h.Name)
				break
			}
		}
		if len(rules.Artists) >= 20 {
			break
		}
	}
	rules.Clean()
	if len(rules.Genres) == 0 && len(rules.Artists) == 0 && rules.YearFrom == 0 && rules.YearTo == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "your library has nothing that fits that; try other words or pick genres by hand"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider": used, "name": clip(out.Name, 80), "description": clip(out.Description, 500), "rules": rules})
}

// handleAsk answers a question about the person's listening (the last 90
// days: stations, channels, times, songs heard).
func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		Question string `json:"question"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Question) == "" {
		writeErr(w, http.StatusBadRequest, "question is required")
		return
	}
	st, _ := s.store.GetSettings(ctx, uid)
	cl, used, why := s.aiFor(ctx, uid, st)
	if cl == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": firstNonEmpty(why, "AI is not set up (Settings → AI)"), "not_configured": true})
		return
	}
	from := store.AddDays(store.Today(), -89)
	f, t := dayBounds(from, store.Today())
	ls, _ := s.store.ListListens(ctx, uid, f, t, 0)
	heard, _ := s.store.ListHeard(ctx, uid, store.HeardFilter{From: f, To: t, Limit: 200})
	favs, _ := s.store.ListStations(ctx, uid, store.StationFilter{Favorites: true})
	chs, _ := s.store.ListChannels(ctx, uid)
	var b strings.Builder
	fmt.Fprintf(&b, "Today is %s.\nFavourite stations: ", store.Today())
	for i, x := range favs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(x.Name)
		if x.Tags != "" {
			b.WriteString(" (" + x.Tags + ")")
		}
	}
	b.WriteString("\nOwn channels: ")
	for i, c := range chs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(c.Name)
	}
	b.WriteString("\nMost listened, last 90 days (minutes): ")
	for i, e := range topOf(ls, 15) {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s %d", e.Name, e.Seconds/60)
	}
	b.WriteString("\nListens (start local time, minutes, station):\n")
	for i, l := range ls {
		if i >= 150 {
			break
		}
		if t, err := time.Parse(time.RFC3339Nano, l.StartedAt); err == nil {
			fmt.Fprintf(&b, "%s %d %s\n", t.In(time.Local).Format("Mon 2006-01-02 15:04"), l.Seconds/60, l.Name)
		}
	}
	b.WriteString("Songs heard (latest first; * = saved):\n")
	for _, h := range heard {
		mark := ""
		if h.Saved {
			mark = "*"
		}
		fmt.Fprintf(&b, "%s%s — on %s, %s\n", mark, h.Title, h.Source, localDay(h.HeardAt))
	}
	system := "You answer questions about a person's radio listening from the facts given. Be brief, plain and concrete; use the numbers. If the facts don't say, say so."
	text, err := cl.Complete(ctx, system, b.String()+"\nQuestion: "+clip(in.Question, 1000), false)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "AI: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider": used, "text": strings.TrimSpace(text)})
}
