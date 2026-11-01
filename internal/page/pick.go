package page

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

// Block is a part of a page that can be watched on its own.
type Block struct {
	Selector string `json:"selector"`
	Label    string `json:"label"` // what kind of part ("Heading", "Price", "Table"…)
	Text     string `json:"text"`  // the first lines of its text
	Lines    int    `json:"lines"`
	Found    bool   `json:"found,omitempty"` // contains the text searched for
}

var (
	reInteresting = regexp.MustCompile(`(?i)price|preis|cijena|cena|stock|avail|lager|status|amount|cost|total|count|date|datum|result|listing|product|offer|badge|title|version|release|news|update|alert|notice|termin|appointment|slot`)
	reGenerated   = regexp.MustCompile(`(?i)^(css|sc|jsx|emotion|svelte|tw|_|chakra|mui|Mui|styled|go\d|ng-)|[0-9a-f]{5,}|\d{3,}|__|--[a-z0-9]{4,}$`)
	reIdentOK     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
)

// Blocks suggests parts of an HTML page worth watching. With find set, the
// smallest parts containing that text come first.
func Blocks(body []byte, find string) ([]Block, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	Strip(doc)
	var out []Block
	seenSel := map[string]bool{}
	seenText := map[string]bool{}
	add := func(n *html.Node, label string, found bool) {
		if len(out) >= 80 {
			return
		}
		text := Clean(NodeText(n))
		if len([]rune(text)) < 2 {
			return
		}
		key := label + "\x00" + text
		if seenText[key] {
			return
		}
		sel := selectorFor(doc, n)
		if sel == "" || seenSel[sel] {
			return
		}
		seenSel[sel], seenText[key] = true, true
		ls := strings.Split(text, "\n")
		excerpt := strings.Join(ls[:min(len(ls), 4)], "\n")
		out = append(out, Block{Selector: sel, Label: label, Text: oneLine2(excerpt, 280), Lines: len(ls), Found: found})
	}
	find = strings.TrimSpace(find)
	if find != "" {
		low := strings.ToLower(find)
		var hits []*html.Node
		doc.Find("body *").Each(func(_ int, s *goquery.Selection) {
			n := s.Nodes[0]
			if !strings.Contains(strings.ToLower(Clean(NodeText(n))), low) {
				return
			}
			// the deepest element: no child element also contains it
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && strings.Contains(strings.ToLower(Clean(NodeText(c))), low) {
					return
				}
			}
			hits = append(hits, n)
		})
		for i, n := range hits {
			if i >= 8 {
				break
			}
			add(n, labelFor(n), true)
			if p := n.Parent; p != nil && p.Type == html.ElementNode && p.Data != "body" {
				add(p, labelFor(p)+" (with what is around it)", true)
			}
		}
	}
	doc.Find(`[itemprop="price"], [itemprop="availability"], [data-price], h1, h2`).Each(func(_ int, s *goquery.Selection) {
		add(s.Nodes[0], labelFor(s.Nodes[0]), false)
	})
	doc.Find("body *").Each(func(_ int, s *goquery.Selection) {
		n := s.Nodes[0]
		id, _ := s.Attr("id")
		cls, _ := s.Attr("class")
		if reInteresting.MatchString(id + " " + cls) {
			add(n, labelFor(n), false)
		}
	})
	doc.Find("main, article, table, h3, [role=main], ul, ol, dl, section").Each(func(_ int, s *goquery.Selection) {
		add(s.Nodes[0], labelFor(s.Nodes[0]), false)
	})
	return out, nil
}

func labelFor(n *html.Node) string {
	id, cls, prop := attr(n, "id"), attr(n, "class"), attr(n, "itemprop")
	hint := strings.ToLower(id + " " + cls + " " + prop)
	switch {
	case strings.Contains(hint, "price") || strings.Contains(hint, "preis") || strings.Contains(hint, "cijena"):
		return "Price"
	case strings.Contains(hint, "stock") || strings.Contains(hint, "avail"):
		return "Availability"
	case strings.Contains(hint, "status"):
		return "Status"
	case strings.Contains(hint, "date") || strings.Contains(hint, "datum"):
		return "Date"
	}
	switch n.Data {
	case "h1", "h2", "h3", "h4":
		return "Heading"
	case "table", "tbody":
		return "Table"
	case "ul", "ol", "dl":
		return "List"
	case "li":
		return "List item"
	case "tr":
		return "Table row"
	case "td", "th":
		return "Table cell"
	case "main", "article":
		return "Main content"
	case "section":
		return "Section"
	case "a":
		return "Link"
	case "button":
		return "Button"
	case "p":
		return "Paragraph"
	case "span", "strong", "b", "em":
		return "Text"
	}
	return "Part of the page"
}

func attr(n *html.Node, k string) string {
	for _, a := range n.Attr {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}

func unique(doc *goquery.Document, sel string, n *html.Node) bool {
	m, err := compileCSS(sel)
	if err != nil {
		return false
	}
	found := doc.FindMatcher(m)
	return found.Length() == 1 && found.Nodes[0] == n
}

// own is a selector for n on its own (id, stable classes, itemprop), "" if none is unique.
func own(doc *goquery.Document, n *html.Node) string {
	if id := attr(n, "id"); id != "" && !reGenerated.MatchString(id) {
		sel := "#" + id
		if !reIdentOK.MatchString(id) {
			sel = fmt.Sprintf(`[id=%q]`, id)
		}
		if unique(doc, sel, n) {
			return sel
		}
	}
	if p := attr(n, "itemprop"); p != "" && reIdentOK.MatchString(p) {
		if sel := fmt.Sprintf(`%s[itemprop="%s"]`, n.Data, p); unique(doc, sel, n) {
			return sel
		}
	}
	var stable []string
	for _, c := range strings.Fields(attr(n, "class")) {
		if reIdentOK.MatchString(c) && !reGenerated.MatchString(c) && len(c) <= 40 {
			stable = append(stable, c)
		}
	}
	for k := 1; k <= min(3, len(stable)); k++ {
		sel := n.Data + "." + strings.Join(stable[:k], ".")
		if unique(doc, sel, n) {
			return sel
		}
	}
	return ""
}

// selectorFor builds a CSS selector that finds exactly n: its own when it
// has one, else a short path from the nearest ancestor that has one.
func selectorFor(doc *goquery.Document, n *html.Node) string {
	if s := own(doc, n); s != "" {
		return s
	}
	var path []string
	cur := n
	for depth := 0; cur != nil && cur.Type == html.ElementNode && depth < 8; depth++ {
		if cur.Data == "body" || cur.Data == "html" {
			path = append([]string{"body"}, path...)
			break
		}
		if depth > 0 {
			if s := own(doc, cur); s != "" {
				path = append([]string{s}, path...)
				break
			}
		}
		path = append([]string{fmt.Sprintf("%s:nth-of-type(%d)", cur.Data, nthOfType(cur))}, path...)
		cur = cur.Parent
	}
	sel := strings.Join(path, " > ")
	if unique(doc, sel, n) {
		return sel
	}
	return ""
}

func nthOfType(n *html.Node) int {
	k := 1
	for s := n.PrevSibling; s != nil; s = s.PrevSibling {
		if s.Type == html.ElementNode && s.Data == n.Data {
			k++
		}
	}
	return k
}

func oneLine2(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
