package server

import (
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-lookout/internal/store"
)

// ---- RSS ----
//
// Every person has a secret feed address (Settings → Notifications): all
// their changes that counted at /feed/<secret>.xml, one watch's at
// /feed/<secret>/<id>.xml. A feed reader (Pocket Reader) needs no login; a
// new secret in Settings stops the old address.

type rss struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Updated     string    `xml:"lastBuildDate,omitempty"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	GUID        rssGUID  `xml:"guid"`
	PubDate     string   `xml:"pubDate"`
	Description string   `xml:"description"`
	Category    []string `xml:"category,omitempty"`
}

type rssGUID struct {
	Perma bool   `xml:"isPermaLink,attr"`
	Value string `xml:",chardata"`
}

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := chi.URLParam(r, "token")
	file := chi.URLParam(r, "file")
	var watchID int64
	if token == "" {
		token = strings.TrimSuffix(file, ".xml")
	} else {
		n, err := strconv.ParseInt(strings.TrimSuffix(file, ".xml"), 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		watchID = n
	}
	uid, err := s.store.FeedOwner(ctx, token)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	title := "Pocket Lookout"
	link := s.link("/changes")
	if watchID > 0 {
		wa, err := s.store.GetWatch(ctx, uid, watchID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		title = wa.Name + " · Pocket Lookout"
		link = s.link(fmt.Sprintf("/watches/%d", wa.ID))
	}
	cs, _ := s.store.ListChanges(ctx, uid, store.ChangeFilter{WatchID: watchID, Limit: 100})
	ch := rssChannel{Title: title, Link: link, Description: "Changes on the pages you watch"}
	for i, c := range cs {
		t, _ := time.Parse(time.RFC3339Nano, c.At)
		if i == 0 {
			ch.Updated = t.Format(time.RFC1123Z)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "<p><b>%s</b></p>", html.EscapeString(c.Summary))
		b.WriteString(`<pre style="white-space:pre-wrap">`)
		for _, l := range c.Diff {
			switch l.Op {
			case "+":
				fmt.Fprintf(&b, `<span style="color:#1a7f37">+ %s</span>`+"\n", html.EscapeString(l.Text))
			case "-":
				fmt.Fprintf(&b, `<span style="color:#cf222e">− %s</span>`+"\n", html.EscapeString(l.Text))
			case "~":
				fmt.Fprintf(&b, "<i>… %s</i>\n", html.EscapeString(l.Text))
			default:
				fmt.Fprintf(&b, "  %s\n", html.EscapeString(l.Text))
			}
		}
		b.WriteString("</pre>")
		fmt.Fprintf(&b, `<p><a href="%s">Open the page</a></p>`, html.EscapeString(c.WatchURL))
		it := rssItem{Title: c.WatchName + ": " + c.Summary, Link: firstNonEmpty(s.link(fmt.Sprintf("/changes/%d", c.ID)), c.WatchURL),
			GUID: rssGUID{Value: fmt.Sprintf("lookout-change-%d", c.ID)}, PubDate: t.Format(time.RFC1123Z), Description: b.String()}
		ch.Items = append(ch.Items, it)
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=60")
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	_ = enc.Encode(rss{Version: "2.0", Channel: ch})
}
