package page

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/abjelosevic88/pocket-homelab-lookout/internal/product"
)

// PriceSelector is the special "selector" that watches the product price and
// availability a shop page publishes (schema.org / Open Graph data).
const PriceSelector = "@price"

// Extracted is what Lookout reads from a page.
type Extracted struct {
	Text  string // the watched text, one line per block
	Title string // the page's title
	Kind  string // html | json | text | other
}

// ErrMissing means the picked part of the page is not there.
type ErrMissing struct{ Selector string }

func (e *ErrMissing) Error() string {
	return fmt.Sprintf("the part of the page you picked (%s) is not there any more", e.Selector)
}

// Extract turns a downloaded page into the text that is watched.
func Extract(r *Result, selector, exclude string) (*Extracted, error) {
	kind := Kind(r.ContentType, r.Body)
	sels := lines(selector)
	switch kind {
	case "json":
		return extractJSON(r.Body, sels)
	case "text":
		if len(sels) > 0 {
			return nil, errors.New("this address is plain text: a part of the page cannot be picked")
		}
		return &Extracted{Text: Clean(string(r.Body)), Kind: kind}, nil
	case "other":
		sum := sha256.Sum256(r.Body)
		return &Extracted{Text: fmt.Sprintf("A file of %d bytes (%s), fingerprint %s", len(r.Body), firstOr(r.ContentType, "unknown type"), hex.EncodeToString(sum[:8])), Kind: kind}, nil
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(r.Body))
	if err != nil {
		return nil, err
	}
	title := Title(doc)
	if len(sels) == 1 && sels[0] == PriceSelector {
		p := product.Parse(string(r.Body), r.FinalURL)
		if p == nil || p.Price == "" {
			return nil, errors.New("the page shows no price a shop publishes for robots (pick the price on the page instead)")
		}
		out := []string{"Price: " + strings.TrimSpace(p.Price+" "+p.Currency)}
		switch p.Availability {
		case "in_stock":
			out = append(out, "Availability: in stock")
		case "out_of_stock":
			out = append(out, "Availability: sold out")
		case "preorder":
			out = append(out, "Availability: pre-order")
		}
		return &Extracted{Text: strings.Join(out, "\n"), Title: firstOr(title, p.Title), Kind: kind}, nil
	}
	Strip(doc)
	for _, ex := range lines(exclude) {
		if sel, err := compile(ex); err == nil {
			doc.FindMatcher(sel).Remove()
		}
	}
	var parts []string
	if len(sels) == 0 {
		parts = append(parts, NodeText(doc.Find("body").Nodes...))
		if strings.TrimSpace(parts[0]) == "" {
			parts[0] = NodeText(doc.Nodes...)
		}
	} else {
		for _, s := range sels {
			sel, err := compile(s)
			if err != nil {
				return nil, err
			}
			found := doc.FindMatcher(sel)
			if found.Length() == 0 {
				return nil, &ErrMissing{s}
			}
			parts = append(parts, NodeText(found.Nodes...))
		}
	}
	return &Extracted{Text: Clean(strings.Join(parts, "\n")), Title: title, Kind: kind}, nil
}

func compile(s string) (goquery.Matcher, error) {
	if strings.HasPrefix(s, "/") || strings.HasPrefix(strings.ToLower(s), "xpath:") {
		return nil, fmt.Errorf("%q looks like XPath; use a CSS selector (like #price or .stock)", s)
	}
	if strings.HasPrefix(s, "$") {
		return nil, fmt.Errorf("%q is a JSON path, but this page is not JSON", s)
	}
	m, err := compileCSS(s)
	if err != nil {
		return nil, fmt.Errorf("%q is not a CSS selector this app understands", s)
	}
	return m, nil
}

// Title is the page's title (or its Open Graph title).
func Title(doc *goquery.Document) string {
	t := strings.TrimSpace(doc.Find("head title").First().Text())
	if t == "" {
		t = strings.TrimSpace(doc.Find("title").First().Text())
	}
	if t == "" {
		t, _ = doc.Find(`meta[property="og:title"]`).Attr("content")
	}
	return oneLine(t, 200)
}

// Strip removes what is never page text.
func Strip(doc *goquery.Document) {
	doc.Find("script, style, noscript, template, svg, iframe, head, link, meta, canvas, [hidden], [aria-hidden=true]").Remove()
}

var blockTags = map[atom.Atom]bool{
	atom.Address: true, atom.Article: true, atom.Aside: true, atom.Blockquote: true, atom.Dd: true, atom.Details: true,
	atom.Div: true, atom.Dl: true, atom.Dt: true, atom.Fieldset: true, atom.Figcaption: true, atom.Figure: true,
	atom.Footer: true, atom.Form: true, atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true,
	atom.H6: true, atom.Header: true, atom.Hr: true, atom.Li: true, atom.Main: true, atom.Nav: true, atom.Ol: true,
	atom.P: true, atom.Pre: true, atom.Section: true, atom.Summary: true, atom.Table: true, atom.Tr: true, atom.Ul: true,
	atom.Option: true, atom.Label: true, atom.Button: true, atom.Caption: true, atom.Tbody: true, atom.Thead: true, atom.Tfoot: true,
}

// NodeText renders nodes as lines of text: blocks on their own lines, table
// cells joined with " | ", list items with a bullet.
func NodeText(nodes ...*html.Node) string {
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(n.Data)
			return
		case html.ElementNode:
			switch n.DataAtom {
			case atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Svg, atom.Head:
				return
			case atom.Br:
				b.WriteString("\n")
				return
			case atom.Img:
				for _, a := range n.Attr {
					if a.Key == "alt" && strings.TrimSpace(a.Val) != "" {
						b.WriteString(" " + strings.TrimSpace(a.Val) + " ")
					}
				}
				return
			case atom.Td, atom.Th:
				b.WriteString(" | ")
			case atom.Li:
				b.WriteString("\n• ")
			case atom.Input:
				// the value of a visible field (a price box, a counter)
				typ, val := "", ""
				for _, a := range n.Attr {
					switch a.Key {
					case "type":
						typ = a.Val
					case "value":
						val = a.Val
					}
				}
				if val != "" && (typ == "" || typ == "text" || typ == "number") {
					b.WriteString(" " + val + " ")
				}
				return
			}
			if blockTags[n.DataAtom] && n.DataAtom != atom.Li {
				b.WriteString("\n")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && blockTags[n.DataAtom] {
			b.WriteString("\n")
		}
	}
	for _, n := range nodes {
		walk(n)
		b.WriteString("\n")
	}
	return b.String()
}

var (
	reSpaces    = regexp.MustCompile(`[ \t\f\r\x{00a0}\x{2000}-\x{200b}\x{202f}\x{205f}\x{3000}]+`)
	reCellEdges = regexp.MustCompile(`^(\|\s*)+|(\s*\|)+$`)
	reCellRuns  = regexp.MustCompile(`(\s*\|\s*){2,}`)
)

// Clean collapses whitespace, trims lines and drops empty ones.
func Clean(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(reSpaces.ReplaceAllString(l, " "))
		if strings.Contains(l, "|") {
			l = reCellRuns.ReplaceAllString(l, " | ")
			l = strings.TrimSpace(reCellEdges.ReplaceAllString(l, ""))
		}
		if l == "" || l == "•" {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// ---- JSON ----

func extractJSON(body []byte, sels []string) (*Extracted, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, errors.New("the page says it is JSON but it does not read as JSON")
	}
	if len(sels) == 0 {
		return &Extracted{Text: pretty(v), Kind: "json"}, nil
	}
	var parts []string
	for _, s := range sels {
		got, err := jsonPath(v, s)
		if err != nil {
			return nil, err
		}
		parts = append(parts, pretty(got))
	}
	return &Extracted{Text: strings.Join(parts, "\n"), Kind: "json"}, nil
}

var rePathPart = regexp.MustCompile(`^([^\[\]]*)((?:\[\d+\])*)$`)

// jsonPath follows "$.a.b[0].c" (dots and [index] only).
func jsonPath(v any, path string) (any, error) {
	p := strings.TrimSpace(path)
	if !strings.HasPrefix(p, "$") {
		return nil, fmt.Errorf("%q: on a JSON page pick a path like $.data.price", path)
	}
	p = strings.TrimPrefix(strings.TrimPrefix(p, "$"), ".")
	cur := v
	if p == "" {
		return cur, nil
	}
	for _, seg := range strings.Split(p, ".") {
		m := rePathPart.FindStringSubmatch(seg)
		if m == nil {
			return nil, fmt.Errorf("%q is not a path this app understands", path)
		}
		if m[1] != "" {
			obj, ok := cur.(map[string]any)
			if !ok {
				return nil, &ErrMissing{path}
			}
			if cur, ok = obj[m[1]]; !ok {
				return nil, &ErrMissing{path}
			}
		}
		for _, idx := range regexp.MustCompile(`\d+`).FindAllString(m[2], -1) {
			i, _ := strconv.Atoi(idx)
			arr, ok := cur.([]any)
			if !ok || i >= len(arr) {
				return nil, &ErrMissing{path}
			}
			cur = arr[i]
		}
	}
	return cur, nil
}

// pretty prints JSON as stable lines (sorted keys, one value per line).
func pretty(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case nil:
		return "null"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(x))
		for _, k := range keys {
			out[k] = x[k]
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		return string(b)
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// ---- small helpers ----

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func firstOr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}
