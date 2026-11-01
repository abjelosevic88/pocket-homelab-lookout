package server

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math/rand"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-lookout/internal/page"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/store"
)

// demoState is what a demo page said from some day on.
type demoState struct {
	days    float64 // days ago
	text    string
	summary string // what the AI said about the change to this state
}

type demoWatch struct {
	w      store.Watch
	states []demoState
	fail   string // the last checks failed with this
	shots  bool
}

func listing(lines ...string) string { return strings.Join(lines, "\n") }

// seedDemo fills the owner's account with watches and months of changes
// (only when the account has no watches yet). With DEMO_DATA the watches are
// not checked against the real sites.
func (s *Server) seedDemo(ctx context.Context, uid int64) error {
	if ws, _ := s.store.ListWatches(ctx, uid, store.WatchFilter{}); len(ws) > 0 {
		return nil
	}
	rng := rand.New(rand.NewSource(42))
	st, _ := s.store.GetSettings(ctx, uid)
	mk := func(url, title, tags string, interval int) store.Watch {
		w := *s.newWatch(st)
		w.URL, w.Title, w.Tags, w.IntervalMinutes = url, title, tags, interval
		return w
	}
	visa := mk("https://sarajevo.diplo.de/ba-de/service/termine", "Visa appointments — German embassy", "errands", 30)
	// the dates ARE the news here: the noise filter would hide them
	visa.TriggerText, visa.Selector, visa.NoiseFilter = "verfügbar\navailable", "#content", false
	visa.AICondition = "Only when an appointment in November or December 2026 shows up"
	flat := mk("https://www.olx.ba/pretraga?kategorija=23&grad=sarajevo&sobe=2", "2-bedroom flats in Sarajevo", "home", 60)
	flat.AICondition, flat.NotifyOn, flat.Selector = "Only if a 2-bedroom under 800 KM a month shows up", "added", ".listings"
	hn := mk("https://news.ycombinator.com/", "Hacker News front page", "tech news", 60)
	hn.Selector, hn.NotifyMode = ".titleline", store.NotifyDigest
	kallax := mk("https://www.ikea.com/ba/hr/p/kallax-polica-bijela-80275887/", "IKEA KALLAX shelf", "shopping", 720)
	kallax.Selector = page.PriceSelector
	outage := mk("https://www.epbih.ba/planirana-iskljucenja", "Planned power outages — Elektroprivreda", "home", 180)
	outage.TriggerText, outage.Selector, outage.NoiseFilter = "Grbavica\nHrasno", "table.iskljucenja", false
	gorel := mk("https://go.dev/doc/devel/release", "Go releases", "tech news, homelab", 720)
	gorel.Selector = "#content h2, #content p"
	jelly := mk("https://github.com/jellyfin/jellyfin/releases", "Jellyfin releases", "homelab", 360)
	jelly.Selector = "section h2"
	kernel := mk("https://www.kernel.org/releases.json", "Linux kernel — latest stable", "tech news", 360)
	kernel.Selector = "$.latest_stable.version"
	pi := mk("https://rpilocator.com/?country=DE&cat=PI5", "Raspberry Pi 5 stock (Germany)", "shopping, homelab", 30)
	pi.Selector, pi.NotifyOn, pi.TriggerText = "#prodTable", "added", "Yes"
	tickets := mk("https://www.dubiozakolektiv.com/tour", "Dubioza Kolektiv tour dates", "fun", 1440)
	tickets.TriggerText, tickets.Selector = "Sarajevo\nMostar", ".tour-dates"
	ts := mk("https://tailscale.com/changelog", "Tailscale changelog", "homelab", 720)
	ts.Selector, ts.NotifyMode = "main h2", store.NotifyDigest
	steam := mk("https://store.steampowered.com/app/1030300/Hollow_Knight_Silksong/", "Hollow Knight: Silksong price", "shopping, fun", 720)
	steam.Selector = page.PriceSelector
	status := mk("https://status.hetzner.com/", "Hetzner status", "homelab", 15)
	status.Selector, status.Screenshot, status.FetchMode = ".incidents", true, store.FetchBrowser
	forum := mk("https://forum.example.org/t/kallax-hacks-2025/1187", "Old forum thread", "", 1440)
	dhl := mk("https://www.dhl.de/de/privatkunden/pakete-empfangen/verfolgen.html?piececode=00340434161094042557", "DHL parcel from Germany", "errands", 120)
	dhl.Paused = true
	holidays := mk("https://www.timeanddate.com/holidays/bosnia/2027", "Public holidays 2027", "errands", 10080)
	holidays.Selector = "#holidays-table"
	py := mk("https://endoflife.date/api/python.json", "Python — latest release", "tech news", 1440)
	py.Selector = "$[0].latest\n$[0].releaseDate"
	shop := mk("https://www.links.ba/proizvod/apple-airpods-pro-2", "AirPods Pro 2 at Links", "shopping", 360)
	shop.Selector, shop.Screenshot, shop.FetchMode = ".product-price", true, store.FetchBrowser
	shop.TriggerText = ""

	watches := []demoWatch{
		{w: visa, states: []demoState{
			{40, listing("Termine für Visa (nationales Visum)", "Derzeit sind keine Termine verfügbar.", "Bitte versuchen Sie es später erneut.", "Stand: 20.09.2026"), ""},
			{12, listing("Termine für Visa (nationales Visum)", "Termine verfügbar: 03.02.2027, 04.02.2027", "Bitte buchen Sie über das Terminportal.", "Stand: 20.10.2026"), "Two appointments opened: 3 and 4 February 2027"},
			{9, listing("Termine für Visa (nationales Visum)", "Derzeit sind keine Termine verfügbar.", "Bitte versuchen Sie es später erneut.", "Stand: 23.10.2026"), "The February appointments are gone; nothing available again"},
			{0.2, listing("Termine für Visa (nationales Visum)", "Termine verfügbar: 26.11.2026, 27.11.2026, 02.12.2026", "Bitte buchen Sie über das Terminportal.", "Stand: 01.11.2026"), "Appointments open on 26 and 27 November and 2 December 2026"},
		}},
		{w: flat, states: []demoState{
			{30, listing("Stan 2 sobe, Grbavica, 58 m² — 900 KM/mj", "Stan 2 sobe, Otoka, 61 m² — 950 KM/mj", "Dvosoban stan, Ciglane, 55 m² — 1.100 KM/mj"), ""},
			{21, listing("Stan 2 sobe, Grbavica, 58 m² — 900 KM/mj", "Stan 2 sobe, Otoka, 61 m² — 950 KM/mj", "Dvosoban stan, Ciglane, 55 m² — 1.100 KM/mj", "Stan 2 sobe, Novo Sarajevo, 64 m² — 980 KM/mj"), "New listing: 2 rooms in Novo Sarajevo, 64 m² for 980 KM"},
			{6, listing("Stan 2 sobe, Alipašino Polje, 52 m² — 750 KM/mj", "Stan 2 sobe, Grbavica, 58 m² — 900 KM/mj", "Stan 2 sobe, Novo Sarajevo, 64 m² — 980 KM/mj", "Dvosoban stan, Ciglane, 55 m² — 1.100 KM/mj"), "New listing: 2 rooms in Alipašino Polje, 52 m² for 750 KM"},
			{1.5, listing("Stan 2 sobe, Alipašino Polje, 52 m² — 750 KM/mj", "Stan 2 sobe, Hrasno, 60 m² — 790 KM/mj", "Stan 2 sobe, Grbavica, 58 m² — 900 KM/mj", "Stan 2 sobe, Novo Sarajevo, 64 m² — 980 KM/mj"), "New listing in Hrasno (60 m², 790 KM); the Ciglane flat is gone"},
		}},
		{w: hn, states: hnStates(rng)},
		{w: kallax, states: []demoState{
			{95, listing("Price: 179.00 BAM", "Availability: in stock"), ""},
			{60, listing("Price: 179.00 BAM", "Availability: sold out"), "Sold out"},
			{48, listing("Price: 169.00 BAM", "Availability: in stock"), "Back in stock and 10 KM cheaper: 169 KM"},
			{3, listing("Price: 149.00 BAM", "Availability: in stock"), "Price dropped from 169 to 149 KM"},
		}},
		{w: outage, states: []demoState{
			{20, listing("Datum | Vrijeme | Područje", "05.11.2026 | 08:00–14:00 | Dobrinja, ulice Bosanska, Mostarska", "06.11.2026 | 09:00–12:00 | Ilidža, Butmir"), ""},
			{8, listing("Datum | Vrijeme | Područje", "05.11.2026 | 08:00–14:00 | Dobrinja, ulice Bosanska, Mostarska", "06.11.2026 | 09:00–12:00 | Ilidža, Butmir", "07.11.2026 | 08:30–13:00 | Grbavica, Zagrebačka 1–40"), "Power off at Grbavica (Zagrebačka 1–40) on 7 November, 08:30–13:00"},
			{2, listing("Datum | Vrijeme | Područje", "06.11.2026 | 09:00–12:00 | Ilidža, Butmir", "07.11.2026 | 08:30–13:00 | Grbavica, Zagrebačka 1–40", "09.11.2026 | 10:00–15:00 | Hrasno, Trg heroja"), "Added: Hrasno (Trg heroja) on 9 November, 10:00–15:00"},
		}},
		{w: gorel, states: []demoState{
			{110, listing("go1.26.0 (released 2026-08-11)", "Go 1.26 is a major release of Go.", "go1.25.0 (released 2025-08-12)"), ""},
			{80, listing("go1.26.1 (released 2026-09-02)", "go1.26.1 includes security fixes to the net/http and crypto/tls packages.", "go1.26.0 (released 2026-08-11)", "Go 1.26 is a major release of Go.", "go1.25.0 (released 2025-08-12)"), "Go 1.26.1 is out with security fixes for net/http and crypto/tls"},
			{45, listing("go1.26.3 (released 2026-09-30)", "go1.26.3 includes fixes to the compiler and the runtime.", "go1.26.1 (released 2026-09-02)", "go1.26.1 includes security fixes to the net/http and crypto/tls packages.", "go1.26.0 (released 2026-08-11)", "Go 1.26 is a major release of Go.", "go1.25.0 (released 2025-08-12)"), "Go 1.26.3 released (compiler and runtime fixes)"},
			{4, listing("go1.26.5 (released 2026-10-28)", "go1.26.5 includes security fixes to the os and net/url packages.", "go1.26.3 (released 2026-09-30)", "go1.26.3 includes fixes to the compiler and the runtime.", "go1.26.1 (released 2026-09-02)", "go1.26.0 (released 2026-08-11)", "Go 1.26 is a major release of Go.", "go1.25.0 (released 2025-08-12)"), "Go 1.26.5 released with security fixes for os and net/url"},
		}},
		{w: jelly, states: []demoState{
			{70, listing("10.11.0", "10.10.7", "10.10.6"), ""},
			{50, listing("10.11.1", "10.11.0", "10.10.7"), "Jellyfin 10.11.1 released"},
			{14, listing("10.11.2", "10.11.1", "10.11.0"), "Jellyfin 10.11.2 released"},
		}},
		{w: kernel, states: []demoState{
			{60, "6.17.2", ""}, {41, "6.17.4", "Latest stable kernel is now 6.17.4"}, {22, "6.17.7", "Latest stable kernel is now 6.17.7"}, {5, "6.18", "Linux 6.18 is the new stable kernel"},
		}},
		{w: pi, states: []demoState{
			{25, listing("Raspberry Pi 5 8GB | Berrybase | No | 89,90 €", "Raspberry Pi 5 4GB | Reichelt | No | 64,50 €", "Raspberry Pi 5 16GB | BuyZero | No | 129,00 €"), ""},
			{18, listing("Raspberry Pi 5 8GB | Berrybase | Yes | 89,90 €", "Raspberry Pi 5 4GB | Reichelt | No | 64,50 €", "Raspberry Pi 5 16GB | BuyZero | No | 129,00 €"), "The 8 GB Pi 5 is in stock at Berrybase for 89.90 €"},
			{17, listing("Raspberry Pi 5 8GB | Berrybase | No | 89,90 €", "Raspberry Pi 5 4GB | Reichelt | No | 64,50 €", "Raspberry Pi 5 16GB | BuyZero | No | 129,00 €"), "Berrybase sold out of the 8 GB model again"},
			{0.6, listing("Raspberry Pi 5 8GB | Berrybase | No | 89,90 €", "Raspberry Pi 5 4GB | Reichelt | No | 64,50 €", "Raspberry Pi 5 16GB | BuyZero | Yes | 124,00 €"), "The 16 GB Pi 5 is in stock at BuyZero for 124 €"},
		}},
		{w: tickets, states: []demoState{
			{90, listing("12.12.2026 — Zagreb, Tvornica kulture", "19.12.2026 — Beograd, Dom omladine"), ""},
			{33, listing("12.12.2026 — Zagreb, Tvornica kulture", "19.12.2026 — Beograd, Dom omladine", "27.12.2026 — Sarajevo, Zetra"), "New concert: Sarajevo, Zetra on 27 December"},
		}},
		{w: ts, states: tsStates()},
		{w: steam, states: []demoState{
			{100, listing("Price: 19.50 EUR"), ""}, {62, listing("Price: 15.60 EUR"), "On sale: 15.60 € (20% off)"}, {55, listing("Price: 19.50 EUR"), "Sale over, back to 19.50 €"},
		}},
		{w: status, shots: true, states: []demoState{
			{14, listing("All systems operational", "No incidents reported today."), ""},
			{3, listing("Degraded performance — Falkenstein (FSN1)", "Investigating: some cloud servers in FSN1 have higher network latency.", "All other systems operational"), "Network latency incident in Falkenstein (FSN1)"},
			{2.7, listing("All systems operational", "Resolved: network latency in FSN1 (2 h 10 min)."), "The FSN1 latency incident is resolved after 2 h 10 min"},
		}},
		{w: forum, fail: "the page is gone (HTTP 404)", states: []demoState{
			{80, listing("KALLAX hacks 2025", "47 replies", "Last post by mirza: legs from the hardware store fit perfectly"), ""},
		}},
		{w: dhl, states: []demoState{
			{9, listing("Die Sendung wurde im Start-Paketzentrum bearbeitet.", "Status: in Zustellung vorbereitet"), ""},
			{7, listing("Die Sendung wurde ins Zielland transportiert und dort an die Zustellorganisation übergeben.", "Status: unterwegs"), "The parcel left Germany and was handed to the local post"},
			{5, listing("Die Sendung wurde erfolgreich zugestellt.", "Status: zugestellt"), "Delivered"},
		}},
		{w: holidays, states: []demoState{
			{120, listing("1 Jan | Friday | New Year's Day", "7 Jan | Thursday | Orthodox Christmas Day", "1 Mar | Monday | Independence Day"), ""},
		}},
		{w: py, states: []demoState{
			{75, listing("3.14.0", "2025-10-07"), ""}, {40, listing("3.14.1", "2025-12-02"), "Python 3.14.1"}, {11, listing("3.15.0", "2026-10-06"), "Python 3.15.0 is out"},
		}},
		{w: shop, shots: true, states: []demoState{
			{30, listing("Apple AirPods Pro 2", "549,00 KM", "Dostupno u radnji"), ""},
			{0.4, listing("Apple AirPods Pro 2", "479,00 KM", "Akcija do 15.11.", "Dostupno u radnji"), "Price dropped from 549 to 479 KM, on sale until 15 November"},
		}},
	}
	now := time.Now()
	for i := range watches {
		dw := &watches[i]
		w := dw.w
		created := now.Add(-time.Duration((dw.states[0].days + 1) * 24 * float64(time.Hour)))
		w.NextCheckAt = now.Add(time.Duration(w.IntervalMinutes) * time.Minute).UTC().Format(time.RFC3339Nano)
		if err := s.store.CreateWatch(ctx, uid, &w); err != nil {
			return err
		}
		_, _ = s.store.DB.ExecContext(ctx, `UPDATE watches SET created_at = ? WHERE id = ?`, created.UTC().Format(time.RFC3339Nano), w.ID)
		ignore := store.Lines(w.IgnoreText)
		var prevText string
		var prevShot []byte
		for k, stt := range dw.states {
			at := now.Add(-time.Duration(stt.days * 24 * float64(time.Hour)))
			// a few quiet checks before this state
			if k > 0 {
				for q := 3; q >= 1; q-- {
					qat := at.Add(-time.Duration(q) * time.Duration(w.IntervalMinutes) * time.Minute)
					cur, _ := s.store.GetWatch(ctx, uid, w.ID)
					_, _ = s.store.RecordCheck(ctx, cur, store.CheckRecord{At: qat, OK: true, Via: via(cur), MS: 300 + rng.Intn(900), Bytes: 40000 + rng.Intn(90000), Status: 200, Interval: time.Hour, KeepSnapshots: 100}, nil)
				}
			}
			cur, _ := s.store.GetWatch(ctx, uid, w.ID)
			rec := store.CheckRecord{At: at, OK: true, Via: via(cur), MS: 300 + rng.Intn(1500), Bytes: 40000 + rng.Intn(90000), Status: 200,
				Interval: time.Duration(w.IntervalMinutes) * time.Minute, KeepSnapshots: 100, Body: stt.text, PageTitle: w.Title}
			b := page.Prepare(stt.text, ignore, w.NoiseFilter)
			rec.Hash = b.Hash()
			var shot []byte
			if dw.shots {
				shot = demoShot(w.ID, k)
				if p, err := s.saveFile(fmt.Sprintf("shots/%d", w.ID), ".jpg", shot); err == nil {
					rec.ShotPath = p
				}
			}
			if k > 0 {
				a := page.Prepare(prevText, ignore, w.NoiseFilter)
				diff, added, removed := page.Diff(a, b, 2)
				c := s.judge(ctx, cur, store.Settings{}, diff, added, removed)
				if stt.summary != "" {
					c.Summary, c.SummaryBy = stt.summary, "ai"
				}
				if w.AICondition != "" {
					c.Reason = demoReason(w.Title, stt.summary)
					if strings.Contains(c.Reason, "does not") {
						c.Matched = false
					}
				}
				if c.Matched && c.Notified != "" {
					c.Notified = "sent"
				}
				if shot != nil && prevShot != nil {
					c.Visual = page.VisualBoxes(prevShot, shot)
				}
				rec.Change = c
			}
			if _, err := s.store.RecordCheck(ctx, cur, rec, nil); err != nil {
				return err
			}
			prevText, prevShot = stt.text, shot
		}
		cur, _ := s.store.GetWatch(ctx, uid, w.ID)
		for q := 5; q >= 1; q-- {
			qat := now.Add(-time.Duration(q) * time.Duration(min(w.IntervalMinutes, 240)) * time.Minute)
			r := store.CheckRecord{At: qat, OK: true, Via: via(cur), MS: 250 + rng.Intn(1200), Bytes: 40000 + rng.Intn(90000), Status: 200,
				Interval: time.Duration(w.IntervalMinutes) * time.Minute, KeepSnapshots: 100}
			if dw.fail != "" && q <= 4 {
				r.OK, r.Error, r.Status, r.RetryIn = false, dw.fail, 404, time.Duration(w.IntervalMinutes)*time.Minute
			}
			_, _ = s.store.RecordCheck(ctx, cur, r, nil)
			cur, _ = s.store.GetWatch(ctx, uid, w.ID)
		}
		if w.Paused {
			_ = s.store.SetPaused(ctx, uid, w.ID, true)
		}
		_, _ = s.store.DB.ExecContext(ctx, `UPDATE watches SET next_check_at = ? WHERE id = ?`, now.Add(time.Duration(w.IntervalMinutes)*time.Minute).UTC().Format(time.RFC3339Nano), w.ID)
	}
	// older changes were looked at; the last two days' are new
	cut := now.Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)
	_, _ = s.store.DB.ExecContext(ctx, `UPDATE changes SET seen = 1 WHERE user_id = ? AND at < ?`, uid, cut)
	_, _ = s.store.DB.ExecContext(ctx, `UPDATE changes SET notified = 'digest' WHERE user_id = ? AND notified = 'sent' AND at >= ? AND watch_id IN (SELECT id FROM watches WHERE notify_mode = 'digest')`, uid, cut)
	_, err := s.store.DB.ExecContext(ctx, `UPDATE watches SET unseen = (SELECT COUNT(1) FROM changes c WHERE c.watch_id = watches.id AND c.seen = 0 AND c.matched = 1) WHERE user_id = ?`, uid)
	s.log.Info("demo data seeded", "watches", len(watches))
	return err
}

func via(w *store.Watch) string {
	if w.FetchMode == store.FetchBrowser {
		return "browser"
	}
	return "fast"
}

func demoReason(title, summary string) string {
	switch {
	case strings.Contains(summary, "Alipašino"):
		return "750 KM for a 2-bedroom is under 800 KM"
	case strings.Contains(summary, "Hrasno"):
		return "Hrasno, 60 m² for 790 KM is under 800 KM"
	case strings.Contains(summary, "Novo Sarajevo"):
		return "980 KM is more than 800 KM, so this does not count"
	case strings.Contains(summary, "February"):
		return "February 2027 is not November or December, so this does not count"
	case strings.Contains(summary, "nothing available"):
		return "no appointment in November or December, so this does not count"
	case strings.Contains(summary, "November"):
		return "26 and 27 November and 2 December 2026 are in the months you asked for"
	}
	return ""
}

func hnStates(rng *rand.Rand) []demoState {
	pool := []string{
		"Show HN: I built a self-hosted change detector in one Go binary",
		"SQLite is all you need for most web apps", "The hidden cost of microservices", "A visual guide to TCP congestion control",
		"Why we moved back to a monolith", "Rust 1.92 released", "Postgres 19 beta: what's new", "The tyranny of the marginal user",
		"How Tailscale's DERP relays work", "An oral history of the Game Boy", "Ask HN: What are you self-hosting in 2026?",
		"Notes on running Linux on an M4 MacBook", "The engineering behind Figma's multiplayer", "Building a CPU in a spreadsheet",
		"Jellyfin 10.11: a year of work", "Writing a database in 1000 lines", "Launch HN: Pocketbase Cloud",
		"Kagi Search hits one million members", "Debian 14 'Forky' freeze announced", "Understanding io_uring",
	}
	var out []demoState
	cur := append([]string{}, pool[:8]...)
	next := 8
	for d := 14.0; d >= 0.3; d -= 1.0 + rng.Float64()*1.5 {
		k := len(out)
		if k > 0 {
			drop := rng.Intn(len(cur))
			added := pool[next%len(pool)]
			next++
			cur = append(cur[:drop], cur[drop+1:]...)
			cur = append([]string{added}, cur...)
			out = append(out, demoState{d, listing(cur...), "New on the front page: “" + added + "”"})
			continue
		}
		out = append(out, demoState{d, listing(cur...), ""})
	}
	return out
}

func tsStates() []demoState {
	v := []string{"Tailscale v1.86.2", "Tailscale v1.86.0", "Tailscale v1.84.3"}
	out := []demoState{{60, listing(v...), ""}}
	for i, d := range []float64{40, 21, 6} {
		nv := fmt.Sprintf("Tailscale v1.%d.%d", 88+2*(i/2), (i%2)*2)
		v = append([]string{nv}, v[:len(v)-1]...)
		out = append(out, demoState{d, listing(v...), nv + " released"})
	}
	return out
}

// demoShot draws a simple page picture; later states move the "news" box
// and repaint the price so the visual compare has something to show.
func demoShot(id int64, state int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 1280, 1400))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{248, 249, 251, 255}}, image.Point{}, draw.Src)
	fill := func(x, y, w, h int, c color.RGBA) {
		draw.Draw(img, image.Rect(x, y, x+w, y+h), &image.Uniform{c}, image.Point{}, draw.Src)
	}
	fill(0, 0, 1280, 72, color.RGBA{14, 116, 144, 255})
	fill(40, 22, 180, 28, color.RGBA{255, 255, 255, 255})
	for i := 0; i < 14; i++ {
		w := 700 + (i*97)%420
		fill(80, 140+i*42, w, 14, color.RGBA{214, 218, 224, 255})
	}
	box := color.RGBA{230, 236, 242, 255}
	if state%2 == 1 {
		box = color.RGBA{255, 228, 214, 255}
	}
	fill(80, 780, 520, 180, box)
	fill(110, 820, 300+state*40, 36, color.RGBA{60, 64, 72, 255})
	if state > 0 {
		fill(700, 780, 500, 120, color.RGBA{209, 250, 229, 255})
		fill(730, 815, 220+state*30, 24, color.RGBA{6, 95, 70, 255})
	}
	for i := 0; i < 6; i++ {
		fill(80, 1020+i*40, 1000-(i*53)%300, 12, color.RGBA{214, 218, 224, 255})
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 70})
	return b.Bytes()
}
