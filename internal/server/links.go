package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core), Radio as the
// target: another Pocket app the person allowed in Core may act here for
// them with a pcx_ token, only within these scopes. Every route not listed
// in linkRoutes stays closed to it (403).

var linkOffers = []core.Offer{
	{Scope: "radio:read", Name: "See what you listen to", Description: "Read your stations, your own channels, what plays now, your listening history and the songs you heard, for your journal, dashboards and recaps."},
	{Scope: "radio:write", Name: "Add stations", Description: "Add an internet radio station to your favourites from another app (the local station of a trip, a station a friend shared). It changes nothing else."},
}

// portable is what Pocket Move may copy out and bring in for a person
// (docs/move.md in pocket-homelab-core); Register adds move:export/import.
var portable = &core.Portable{
	Exports: []core.PortableExport{
		{Path: "/api/v1/export.json", File: "radio.json", Kind: core.KindPocket, Title: "Your stations, channels, listening history and the songs you heard"},
		{Path: "/api/v1/export.csv", File: "listening.csv", Kind: core.KindCSV, Title: "Your listening history as a table"},
		{Path: "/api/v1/stations/export.m3u", File: "stations.m3u", Kind: "m3u", Title: "Your stations as an M3U playlist for any radio player"},
	},
	Imports: []core.PortableImport{
		{Path: "/api/v1/import", Kind: core.KindPocket, ContentType: "application/json", Title: "Adds the stations, channels, listening history and songs heard from another box; what you already have is left alone"},
		{Path: "/api/v1/stations/import", Kind: "m3u", ContentType: "audio/x-mpegurl", Title: "Adds the stations in an M3U or PLS playlist; stations you already have are skipped"},
	},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string // "" = any linked app
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(dashboard|now|history|stations|channels|heard)$`), "radio:read"},
	{"GET", regexp.MustCompile(`^/api/v1/(day|summary)/\d{4}-\d{2}-\d{2}$`), "radio:read"},
	{"GET", regexp.MustCompile(`^/api/v1/(stations|channels)/\d+$`), "radio:read"},
	{"POST", regexp.MustCompile(`^/api/v1/stations$`), "radio:write"},
}

var errLinkScope = errors.New("outside the link's scope")

// linkAllows reports whether a link token with these scopes may make this request.
func linkAllows(scopes []string, r *http.Request) bool {
	if portable.LinkAllows(scopes, r) {
		return true
	}
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// linkUser validates a pcx_ token, checks the route against its scopes and
// maps it to the person it acts for. The second value is the acting app's slug.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, string, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, "", err
	}
	if !in.Active || !in.Delegated() || in.Audience != "radio" || in.User == nil || in.User.Email == "" || in.App == nil || in.App.Slug == "" {
		return nil, "", http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, "", errLinkScope
	}
	u, err := s.coreIdentityUser(ctx, in)
	return u, in.App.Slug, err
}

const linkAppKey ctxKey = 2

// linkApp is the acting app's slug when the request came in on a link token.
func linkApp(ctx context.Context) string {
	a, _ := ctx.Value(linkAppKey).(string)
	return a
}
