package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/store"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core), Lookout as
// the target: another Pocket app the person allowed in Core may act here for
// them with a pcx_ token, only within these scopes. Every route not listed in
// linkRoutes stays closed to it (403). A watch it adds carries the app's slug
// as source, and it may change, check or delete only those.

var linkOffers = []core.Offer{
	{Scope: "watches:read", Name: "See your watched pages", Description: "Read the pages you watch and what changed on them (with the one-line summaries), for your journal, dashboards and automations."},
	{Scope: "watches:write", Name: "Watch pages for you", Description: "Start watching a page for you (e.g. from an automation or a chat), and change, check or delete only the watches that app added itself."},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string // "" = any linked app
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(watches|changes|dashboard|tags)$`), "watches:read"},
	{"GET", regexp.MustCompile(`^/api/v1/(watches|changes)/\d+$`), "watches:read"},
	{"GET", regexp.MustCompile(`^/api/v1/watches/\d+/(changes|checks|snapshot)$`), "watches:read"},
	{"GET", regexp.MustCompile(`^/api/v1/day/\d{4}-\d{2}-\d{2}$`), "watches:read"},
	{"POST", regexp.MustCompile(`^/api/v1/watches$`), "watches:write"},
	{"POST", regexp.MustCompile(`^/api/v1/preview$`), "watches:write"},
	{"PUT DELETE", regexp.MustCompile(`^/api/v1/watches/\d+$`), "watches:write"},
	{"POST", regexp.MustCompile(`^/api/v1/watches/\d+/(check|pause|resume)$`), "watches:write"},
}

var errLinkScope = errors.New("outside the link's scope")

// linkAllows reports whether a link token with these scopes may make this request.
func linkAllows(scopes []string, r *http.Request) bool {
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
	if !in.Active || !in.Delegated() || in.Audience != "lookout" || in.User == nil || in.User.Email == "" || in.App == nil || in.App.Slug == "" {
		return nil, "", http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, "", errLinkScope
	}
	u, err := s.coreIdentityUser(ctx, in)
	return u, in.App.Slug, err
}

const linkAppKey ctxKey = 2

// linkApp is the acting app's slug when the request came in on a link token,
// "" otherwise.
func linkApp(ctx context.Context) string {
	a, _ := ctx.Value(linkAppKey).(string)
	return a
}

// mayChange: a person may change all their watches, a linked app only the
// ones it added.
func mayChange(ctx context.Context, w *store.Watch) bool {
	app := linkApp(ctx)
	return app == "" || w.Source == app
}
