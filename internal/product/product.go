// Package product reads a shop's product page: name, picture, price,
// currency and whether it is in stock. It understands schema.org Product
// data (JSON-LD and microdata) and the Open Graph / product meta tags most
// shops publish for link previews, and falls back to the page title.
package product

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Product is what a page says about the thing it sells.
type Product struct {
	URL          string `json:"url"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Image        string `json:"image"`
	Store        string `json:"store"`
	Brand        string `json:"brand"`
	Price        string `json:"price"` // as written on the page, normalised to "1234.56"
	Currency     string `json:"currency"`
	Availability string `json:"availability"` // in_stock | out_of_stock | preorder | ""

	// Delivery: countries the shop's structured data names (ISO codes),
	// shipping rates per country, sentences of the page about shipping,
	// and links to the shop's shipping / delivery page.
	ShipsTo   []string   `json:"ships_to,omitempty"`
	ShipRates []ShipRate `json:"ship_rates,omitempty"`
	ShipText  []string   `json:"ship_text,omitempty"`
	ShipLinks []string   `json:"ship_links,omitempty"`
}

// ShipRate is what delivery to one country costs ("" country = everywhere).
type ShipRate struct {
	Country  string `json:"country"`
	Price    string `json:"price"`
	Currency string `json:"currency"`
}

// ErrBlocked is returned for addresses the fetcher refuses (private networks).
var ErrBlocked = errors.New("that address is on a private network")

// Fetcher downloads pages and pictures.
type Fetcher struct {
	UserAgent    string
	AllowPrivate bool
	Timeout      time.Duration
}

func (f *Fetcher) client() *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if !f.AllowPrivate {
		d.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip != nil && privateIP(ip) {
				return ErrBlocked
			}
			return nil
		}
	}
	t := &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second}
	timeout := f.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{Transport: t, Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 8 {
			return errors.New("too many redirects")
		}
		return nil
	}}
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func privateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || cgnat.Contains(ip)
}

// CheckURL accepts http(s) URLs only.
func CheckURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("not a web address (it needs http:// or https://)")
	}
	return u, nil
}

func (f *Fetcher) get(ctx context.Context, raw, accept string, limit int64) ([]byte, string, string, error) {
	u, err := CheckURL(raw)
	if err != nil {
		return nil, "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", "", err
	}
	ua := f.UserAgent
	if ua == "" {
		ua = "Mozilla/5.0 (compatible; PocketLookout/1.0)"
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "en,de;q=0.8,*;q=0.5")
	res, err := f.client().Do(req)
	if err != nil {
		if errors.Is(err, ErrBlocked) {
			return nil, "", "", ErrBlocked
		}
		return nil, "", "", err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return nil, "", "", fmt.Errorf("the shop answered HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit))
	return b, res.Header.Get("Content-Type"), res.Request.URL.String(), err
}

// Fetch downloads a product page and reads it.
func (f *Fetcher) Fetch(ctx context.Context, raw string) (*Product, error) {
	b, ct, final, err := f.get(ctx, raw, "text/html,application/xhtml+xml;q=0.9,*/*;q=0.5", 6<<20)
	if err != nil {
		return nil, err
	}
	if mt, _, _ := mime.ParseMediaType(ct); mt != "" && !strings.Contains(mt, "html") && !strings.Contains(mt, "xml") {
		return nil, fmt.Errorf("that link is not a web page (%s)", mt)
	}
	p := Parse(string(b), final)
	if p.Title == "" && p.Price == "" {
		return p, errors.New("the page says nothing about a product")
	}
	return p, nil
}

// Image downloads a picture (up to limit bytes) and returns it with its file extension.
func (f *Fetcher) Image(ctx context.Context, raw string, limit int64) ([]byte, string, error) {
	b, ct, _, err := f.get(ctx, raw, "image/avif,image/webp,image/png,image/jpeg,image/*;q=0.8", limit+1)
	if err != nil {
		return nil, "", err
	}
	if int64(len(b)) > limit {
		return nil, "", errors.New("the picture is too large")
	}
	mt, _, _ := mime.ParseMediaType(ct)
	if mt == "" || mt == "application/octet-stream" {
		mt = http.DetectContentType(b)
	}
	ext := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif", "image/avif": ".avif", "image/svg+xml": ".svg"}[mt]
	if ext == "" {
		return nil, "", fmt.Errorf("not a picture (%s)", mt)
	}
	return b, ext, nil
}

var (
	reScript   = regexp.MustCompile(`(?is)<script[^>]+type\s*=\s*["']?application/ld\+json["']?[^>]*>(.*?)</script>`)
	reMeta     = regexp.MustCompile(`(?is)<meta\s[^>]*>`)
	reAttr     = regexp.MustCompile(`(?is)([a-zA-Z_:.-]+)\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))`)
	reTitle    = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reItemprop = regexp.MustCompile(`(?is)<[a-z]+\s[^>]*itemprop\s*=\s*["']?(price|priceCurrency|availability|lowPrice)["']?[^>]*>`)
	reSpace    = regexp.MustCompile(`\s+`)
	reTags     = regexp.MustCompile(`(?s)<[^>]*>`)
)

func attrs(tag string) map[string]string {
	out := map[string]string{}
	for _, m := range reAttr.FindAllStringSubmatch(tag, -1) {
		v := m[3]
		if v == "" {
			v = m[4]
		}
		if v == "" {
			v = m[5]
		}
		out[strings.ToLower(m[1])] = html.UnescapeString(v)
	}
	return out
}

func clean(s string) string {
	s = html.UnescapeString(reTags.ReplaceAllString(s, " "))
	return strings.TrimSpace(reSpace.ReplaceAllString(s, " "))
}

// Parse reads a product page's HTML. pageURL resolves relative pictures.
func Parse(page, pageURL string) *Product {
	p := &Product{URL: pageURL, Store: StoreName(pageURL)}
	// 1. schema.org Product in JSON-LD
	for _, m := range reScript.FindAllStringSubmatch(page, -1) {
		var v any
		if json.Unmarshal([]byte(strings.TrimSpace(m[1])), &v) != nil {
			continue
		}
		if n := findProduct(v); n != nil {
			fromNode(p, n)
			shippingFrom(p, n)
			break
		}
	}
	// 2. meta tags (Open Graph, product:*, twitter:*), microdata
	meta := map[string]string{}
	for _, tag := range reMeta.FindAllString(page, -1) {
		a := attrs(tag)
		key := strings.ToLower(firstOf(a["property"], a["name"], a["itemprop"]))
		if key != "" && a["content"] != "" {
			if _, ok := meta[key]; !ok {
				meta[key] = strings.TrimSpace(a["content"])
			}
		}
	}
	for _, tag := range reItemprop.FindAllString(page, -1) {
		a := attrs(tag)
		key := strings.ToLower(a["itemprop"])
		v := firstOf(a["content"], a["href"], a["value"])
		if v != "" {
			if _, ok := meta["itemprop:"+key]; !ok {
				meta["itemprop:"+key] = v
			}
		}
	}
	if p.Title == "" {
		p.Title = firstOf(meta["og:title"], meta["twitter:title"], meta["name"])
	}
	if p.Title == "" {
		if m := reTitle.FindStringSubmatch(page); m != nil {
			p.Title = clean(m[1])
		}
	}
	if p.Description == "" {
		p.Description = firstOf(meta["og:description"], meta["description"], meta["twitter:description"])
	}
	if p.Image == "" {
		p.Image = firstOf(meta["og:image:secure_url"], meta["og:image"], meta["twitter:image"], meta["twitter:image:src"], meta["image"])
	}
	if s := meta["og:site_name"]; s != "" {
		p.Store = s
	}
	if p.Brand == "" {
		p.Brand = firstOf(meta["product:brand"], meta["og:brand"])
	}
	if p.Price == "" {
		p.Price = NormalizePrice(firstOf(meta["product:price:amount"], meta["og:price:amount"], meta["product:sale_price:amount"], meta["itemprop:price"], meta["itemprop:lowprice"], meta["price"]))
	}
	if p.Currency == "" {
		p.Currency = strings.ToUpper(firstOf(meta["product:price:currency"], meta["og:price:currency"], meta["itemprop:pricecurrency"], meta["pricecurrency"]))
	}
	if p.Availability == "" {
		p.Availability = availability(firstOf(meta["product:availability"], meta["og:availability"], meta["itemprop:availability"]))
	}
	p.Title = clean(p.Title)
	p.Description = clean(p.Description)
	if len(p.Description) > 600 {
		p.Description = strings.TrimSpace(p.Description[:600]) + "…"
	}
	p.Image = absolute(pageURL, p.Image)
	p.ShipText = shipSentences(page)
	p.ShipLinks = shipLinks(page, pageURL)
	if len(p.Currency) != 3 {
		p.Currency = currencyFromSymbol(p.Currency)
	}
	return p
}

func firstOf(v ...string) string {
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

func absolute(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	return b.ResolveReference(r).String()
}

// StoreName is a shop's readable name from its address ("www.amazon.de" → "amazon.de").
func StoreName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	for _, p := range []string{"www.", "m.", "shop.", "store."} {
		if strings.HasPrefix(h, p) && strings.Count(h, ".") > 1 {
			return strings.TrimPrefix(h, p)
		}
	}
	return h
}

func isType(v any, want ...string) bool {
	check := func(s string) bool {
		s = strings.TrimPrefix(strings.TrimPrefix(s, "http://schema.org/"), "https://schema.org/")
		for _, w := range want {
			if strings.EqualFold(s, w) {
				return true
			}
		}
		return false
	}
	switch t := v.(type) {
	case string:
		return check(t)
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok && check(s) {
				return true
			}
		}
	}
	return false
}

func findProduct(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		if isType(t["@type"], "Product", "ProductGroup", "IndividualProduct", "Book", "Vehicle", "SoftwareApplication") {
			return t
		}
		for _, k := range []string{"@graph", "mainEntity", "itemListElement", "item"} {
			if n := findProduct(t[k]); n != nil {
				return n
			}
		}
	case []any:
		for _, x := range t {
			if n := findProduct(x); n != nil {
				return n
			}
		}
	}
	return nil
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	case map[string]any:
		return firstOf(str(t["name"]), str(t["url"]), str(t["@id"]))
	case []any:
		for _, x := range t {
			if s := str(x); s != "" {
				return s
			}
		}
	}
	return ""
}

func image(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		return firstOf(str(t["url"]), str(t["contentUrl"]))
	case []any:
		for _, x := range t {
			if s := image(x); s != "" {
				return s
			}
		}
	}
	return ""
}

func fromNode(p *Product, n map[string]any) {
	p.Title = str(n["name"])
	p.Description = str(n["description"])
	p.Image = image(n["image"])
	p.Brand = str(n["brand"])
	offers := n["offers"]
	if offers == nil {
		if vars, ok := n["hasVariant"].([]any); ok && len(vars) > 0 {
			if first, ok := vars[0].(map[string]any); ok {
				offers = first["offers"]
			}
		}
	}
	price, cur, avail := offer(offers)
	p.Price, p.Currency, p.Availability = NormalizePrice(price), strings.ToUpper(cur), availability(avail)
}

// offer picks the price from an Offer, AggregateOffer or a list of offers
// (the lowest one in the first currency seen).
func offer(v any) (price, cur, avail string) {
	switch t := v.(type) {
	case map[string]any:
		price = firstOf(str(t["price"]), str(t["lowPrice"]))
		cur = str(t["priceCurrency"])
		avail = str(t["availability"])
		if ps, ok := t["priceSpecification"]; ok && price == "" {
			pp, pc, _ := offer(ps)
			price, cur = pp, firstOf(cur, pc)
		}
		if price == "" {
			if inner, ok := t["offers"]; ok {
				return offer(inner)
			}
		}
	case []any:
		best := -1.0
		for _, x := range t {
			p, c, a := offer(x)
			f, err := strconv.ParseFloat(NormalizePrice(p), 64)
			if err != nil || f <= 0 || (cur != "" && c != "" && !strings.EqualFold(c, cur)) {
				continue
			}
			if best < 0 || f < best {
				best, price, cur, avail = f, p, firstOf(c, cur), a
			}
		}
	}
	return
}

func availability(v string) string {
	v = strings.ToLower(v)
	switch {
	case v == "":
		return ""
	case strings.Contains(v, "outofstock"), strings.Contains(v, "out of stock"), strings.Contains(v, "soldout"), strings.Contains(v, "discontinued"), v == "oos":
		return "out_of_stock"
	case strings.Contains(v, "preorder"), strings.Contains(v, "presale"), strings.Contains(v, "backorder"):
		return "preorder"
	case strings.Contains(v, "instock"), strings.Contains(v, "in stock"), strings.Contains(v, "limitedavailability"), strings.Contains(v, "onlineonly"), strings.Contains(v, "instoreonly"):
		return "in_stock"
	}
	return ""
}

var rePriceChars = regexp.MustCompile(`[^0-9.,]`)

// NormalizePrice turns "1.299,00 €", "$1,299.00" or "12,5" into "1299.00" /
// "12.5" ("" when there is no number).
func NormalizePrice(s string) string {
	s = rePriceChars.ReplaceAllString(strings.TrimSpace(s), "")
	if s == "" {
		return ""
	}
	dot, comma := strings.LastIndex(s, "."), strings.LastIndex(s, ",")
	switch {
	case dot >= 0 && comma >= 0:
		if comma > dot { // 1.299,00
			s = strings.ReplaceAll(s, ".", "")
			s = strings.Replace(s, ",", ".", 1)
		} else { // 1,299.00
			s = strings.ReplaceAll(s, ",", "")
		}
	case comma >= 0:
		// "12,99" is a decimal comma; "1,299" (three digits after, one comma) is thousands
		if len(s)-comma-1 == 3 && strings.Count(s, ",") >= 1 && !strings.HasPrefix(s, "0,") {
			s = strings.ReplaceAll(s, ",", "")
		} else {
			s = strings.ReplaceAll(s, ",", ".")
		}
	case strings.Count(s, ".") > 1:
		s = strings.ReplaceAll(s, ".", "")
	}
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return ""
	}
	return s
}

// CurrencyFromSymbol maps "€", "KM", "$"… (and ISO codes) to an ISO code ("" = unknown).
func CurrencyFromSymbol(s string) string {
	if t := strings.ToUpper(strings.TrimSpace(s)); len(t) == 3 && t != "KN." {
		switch t {
		case "EUR", "USD", "GBP", "BAM", "CHF", "RSD", "HRK", "PLN", "CZK", "HUF", "SEK", "NOK", "DKK", "RON", "BGN", "TRY", "JPY", "CAD", "AUD":
			return t
		}
	}
	return currencyFromSymbol(s)
}

func currencyFromSymbol(s string) string {
	switch strings.TrimSpace(s) {
	case "€":
		return "EUR"
	case "$", "US$":
		return "USD"
	case "£":
		return "GBP"
	case "KM", "km", "Km":
		return "BAM"
	case "din", "din.", "RSD":
		return "RSD"
	case "¥":
		return "JPY"
	case "zł":
		return "PLN"
	case "kn":
		return "HRK"
	case "Kč":
		return "CZK"
	case "CHF", "Fr.":
		return "CHF"
	}
	return ""
}

// ToMinor converts a normalised price ("12.5") to minor units with the given decimals.
func ToMinor(price string, decimals int) int64 {
	f, err := strconv.ParseFloat(NormalizePrice(price), 64)
	if err != nil || f <= 0 {
		return 0
	}
	mul := 1.0
	for i := 0; i < decimals; i++ {
		mul *= 10
	}
	return int64(f*mul + 0.5)
}

// ---- delivery ----

// shipWords are what shops write about delivery, in the languages around Europe.
var shipWords = []string{"ship", "deliver", "delivery", "versand", "liefer", "livraison", "livrons", "spedizion", "consegn", "envío", "envio",
	"enviamos", "dostav", "isporu", "wysyłk", "dostaw", "verzend", "bezorg", "doprava", "doruč", "szállít", "leverans", "levering", "expedi"}

var (
	reScriptStyle = regexp.MustCompile(`(?is)<(script|style|noscript|svg)[^>]*>.*?</(script|style|noscript|svg)>`)
	reAnchor      = regexp.MustCompile(`(?is)<a\s[^>]*href\s*=\s*["']([^"'#]+)["'][^>]*>(.*?)</a>`)
	reNav         = regexp.MustCompile(`(?is)<nav[^>]*>.*?</nav>`)
	reSentence    = regexp.MustCompile(`[^.!?\n]{12,260}[.!?\n]?`)
)

// PlainText is a page's visible text (scripts and styles removed).
func PlainText(page string) string {
	page = reScriptStyle.ReplaceAllString(page, " ")
	page = regexp.MustCompile(`(?i)<br\s*/?>|</(p|div|li|tr|h[1-6])>`).ReplaceAllString(page, "\n")
	return html.UnescapeString(reTags.ReplaceAllString(page, " "))
}

func mentionsShipping(s string) bool {
	low := strings.ToLower(s)
	for _, w := range shipWords {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}

// shipSentences keeps up to eight short sentences of the page about delivery.
func shipSentences(page string) []string {
	// menus and links are labels, not statements about delivery
	page = reNav.ReplaceAllString(page, " ")
	page = reAnchor.ReplaceAllString(page, " ")
	text := PlainText(page)
	if len(text) > 400_000 {
		text = text[:400_000]
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range reSentence.FindAllString(text, -1) {
		m = strings.TrimSpace(reSpace.ReplaceAllString(m, " "))
		if len(strings.Fields(m)) < 5 || !mentionsShipping(m) || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
		if len(out) == 8 {
			break
		}
	}
	return out
}

// shipLinks finds up to three links to the shop's own shipping / delivery page.
func shipLinks(page, pageURL string) []string {
	base, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range reAnchor.FindAllStringSubmatch(page, -1) {
		href, text := html.UnescapeString(m[1]), clean(m[2])
		probe := strings.ToLower(href + " " + text)
		if !mentionsShipping(probe) || strings.Contains(probe, "tracking") || strings.Contains(probe, "sendungsverfolgung") {
			continue
		}
		u, err := base.Parse(href)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || StoreName(u.String()) != StoreName(pageURL) {
			continue
		}
		u.Fragment = ""
		if !seen[u.String()] && u.String() != pageURL {
			seen[u.String()] = true
			out = append(out, u.String())
			if len(out) == 3 {
				break
			}
		}
	}
	return out
}

// countryCode reads a schema.org country: "DE", "Germany" is not mapped (codes only), {"name":"DE"}.
func countryCodes(v any) []string {
	var out []string
	switch t := v.(type) {
	case string:
		for _, part := range strings.FieldsFunc(t, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
			if c := strings.ToUpper(strings.TrimSpace(part)); len(c) == 2 {
				out = append(out, c)
			}
		}
	case map[string]any:
		for _, k := range []string{"addressCountry", "name", "identifier", "@id"} {
			if x, ok := t[k]; ok {
				if cs := countryCodes(x); len(cs) > 0 {
					return cs
				}
			}
		}
	case []any:
		for _, x := range t {
			out = append(out, countryCodes(x)...)
		}
	}
	return out
}

// shippingFrom reads schema.org OfferShippingDetails / eligibleRegion of a product.
func shippingFrom(p *Product, n map[string]any) {
	seen := map[string]bool{}
	addCountry := func(c string) {
		if !seen[c] {
			seen[c] = true
			p.ShipsTo = append(p.ShipsTo, c)
		}
	}
	var walk func(v any, depth int)
	walk = func(v any, depth int) {
		if depth > 6 {
			return
		}
		switch t := v.(type) {
		case []any:
			for _, x := range t {
				walk(x, depth+1)
			}
		case map[string]any:
			if sd, ok := t["shippingDetails"]; ok {
				details := []any{sd}
				if arr, ok := sd.([]any); ok {
					details = arr
				}
				for _, d := range details {
					dm, ok := d.(map[string]any)
					if !ok {
						continue
					}
					cs := countryCodes(dm["shippingDestination"])
					for _, c := range cs {
						addCountry(c)
					}
					if rate, ok := dm["shippingRate"].(map[string]any); ok {
						price, cur := NormalizePrice(str(rate["value"])), strings.ToUpper(str(rate["currency"]))
						if price != "" {
							if len(cs) == 0 {
								cs = []string{""}
							}
							for _, c := range cs {
								p.ShipRates = append(p.ShipRates, ShipRate{Country: c, Price: price, Currency: cur})
							}
						}
					}
				}
			}
			for _, k := range []string{"eligibleRegion", "areaServed"} {
				if r, ok := t[k]; ok {
					for _, c := range countryCodes(r) {
						addCountry(c)
					}
				}
			}
			for _, k := range []string{"offers", "hasVariant"} {
				if x, ok := t[k]; ok {
					walk(x, depth+1)
				}
			}
		}
	}
	walk(n, 0)
}

// FetchText downloads a page (a shop's shipping page) and returns its visible text.
func (f *Fetcher) FetchText(ctx context.Context, raw string) (string, error) {
	b, _, _, err := f.get(ctx, raw, "text/html,application/xhtml+xml;q=0.9,*/*;q=0.5", 3<<20)
	if err != nil {
		return "", err
	}
	return strings.Join(strings.Fields(PlainText(string(b))), " "), nil
}
