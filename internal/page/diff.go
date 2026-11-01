package page

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
)

func compileCSS(s string) (goquery.Matcher, error) { return cascadia.Compile(s) }

// ---- noise ----

var noise = []struct {
	re   *regexp.Regexp
	repl string
}{
	// ISO timestamps and dates
	{regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?)?\b`), "‹date›"},
	// 1.11.2026, 11/01/2026, 01-11-26
	{regexp.MustCompile(`\b\d{1,2}[./-]\d{1,2}[./-]\d{2,4}\.?`), "‹date›"},
	// Nov 1, 2026 · November 1st 2026 · 1 November 2026 · 1. novembar 2026.
	{regexp.MustCompile(`(?i)\b(?:jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\.?\s+\d{1,2}(?:st|nd|rd|th)?,?\s+\d{4}\b`), "‹date›"},
	{regexp.MustCompile(`(?i)\b\d{1,2}(?:st|nd|rd|th)?\.?\s+(?:jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\.?,?\s+\d{4}\b\.?`), "‹date›"},
	// times: 12:34, 12:34:56, 9:05 pm
	{regexp.MustCompile(`(?i)\b\d{1,2}:\d{2}(?::\d{2})?(?:\s?[ap]\.?m\.?)?\b`), "‹time›"},
	// relative times: 5 minutes ago, 3h ago, vor 2 Stunden, prije 3 sata
	{regexp.MustCompile(`(?i)\b\d+\s*(?:seconds?|secs?|minutes?|mins?|hours?|hrs?|days?|weeks?|months?|years?|s|m|h|d|w|mo|y)\s+ago\b`), "‹ago›"},
	{regexp.MustCompile(`(?i)\b(?:vor|prije|pre)\s+\d+\s*\p{L}+`), "‹ago›"},
	{regexp.MustCompile(`(?i)\b(?:just now|a few seconds ago|a minute ago|an hour ago|a day ago|yesterday|today)\b`), "‹ago›"},
	// counters: 1,234 views · 12k likes · 3 comments · 120 watching now
	{regexp.MustCompile(`(?i)\b\d[\d,.\s]*[kmb]?\s*(views?|likes?|comments?|followers?|subscribers?|shares?|reviews?|ratings?|watching|online|visitors?|downloads?|stars?|replies|reactions?|votes?|plays?|listeners?|readers?|people (?:are )?(?:viewing|looking))\b`), "‹n› $1"},
}

// NoiseKey is a line with dates, times, "ago"s and view/like counters
// replaced, so two lines that differ only in those compare equal.
func NoiseKey(line string) string {
	for _, n := range noise {
		line = n.re.ReplaceAllString(line, n.repl)
	}
	return line
}

// ---- rules: ignore / trigger lines ----

var reCache sync.Map // rule → *regexp.Regexp (nil = not a regex)

func ruleRegex(rule string) *regexp.Regexp {
	if v, ok := reCache.Load(rule); ok {
		re, _ := v.(*regexp.Regexp)
		return re
	}
	var re *regexp.Regexp
	if len(rule) > 2 && strings.HasPrefix(rule, "/") {
		body, flags := rule[1:], ""
		if i := strings.LastIndex(body, "/"); i >= 0 {
			body, flags = body[:i], body[i+1:]
		}
		if !strings.Contains(flags, "c") { // case-sensitive only when asked
			body = "(?i)" + body
		}
		re, _ = regexp.Compile(body)
	}
	reCache.Store(rule, re)
	return re
}

// ValidRule says what is wrong with a rule ("" = fine).
func ValidRule(rule string) string {
	rule = strings.TrimSpace(rule)
	if len(rule) > 2 && strings.HasPrefix(rule, "/") && ruleRegex(rule) == nil {
		return fmt.Sprintf("%q is not a valid pattern", rule)
	}
	return ""
}

// Matches reports whether a line matches a rule: a /regular expression/ or,
// otherwise, the text anywhere in the line (case does not matter).
func Matches(line, rule string) bool {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return false
	}
	if re := ruleRegex(rule); re != nil {
		return re.MatchString(line)
	}
	return strings.Contains(strings.ToLower(line), strings.ToLower(rule))
}

// MatchesAny reports the first rule the line matches ("" = none).
func MatchesAny(line string, rules []string) string {
	for _, r := range rules {
		if Matches(line, r) {
			return r
		}
	}
	return ""
}

// Compare is the text that is compared: lines matching an ignore rule left
// out; with the noise filter on, each line's NoiseKey next to it.
type Compare struct {
	Lines []string // what is shown
	Keys  []string // what is compared
}

// Prepare splits a snapshot into compared lines.
func Prepare(text string, ignore []string, noiseFilter bool) Compare {
	var c Compare
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == "" || MatchesAny(l, ignore) != "" {
			continue
		}
		k := l
		if noiseFilter {
			k = NoiseKey(l)
		}
		c.Lines = append(c.Lines, l)
		c.Keys = append(c.Keys, k)
	}
	return c
}

// Hash fingerprints the compared text.
func (c Compare) Hash() string {
	h := sha256.Sum256([]byte(strings.Join(c.Keys, "\n")))
	return hex.EncodeToString(h[:16])
}

// ---- diff ----

// Line is one line of a diff: "+" added, "-" removed, "=" unchanged, "~" a
// gap of unchanged lines (Text says how many).
type Line struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// Diff compares two prepared texts line by line and returns the changes
// with `context` unchanged lines around them, and the counts.
func Diff(a, b Compare, context int) (out []Line, added, removed int) {
	ops := diffOps(a.Keys, b.Keys)
	// ops index into a (for "-" and "=") or b (for "+")
	type op struct {
		kind string
		i    int
	}
	var all []op
	for _, o := range ops {
		all = append(all, op{o.kind, o.i})
		switch o.kind {
		case "+":
			added++
		case "-":
			removed++
		}
	}
	if added == 0 && removed == 0 {
		return nil, 0, 0
	}
	keep := make([]bool, len(all))
	for i, o := range all {
		if o.kind != "=" {
			for j := max(0, i-context); j <= min(len(all)-1, i+context); j++ {
				keep[j] = true
			}
		}
	}
	gap := 0
	flush := func() {
		if gap > 0 {
			out = append(out, Line{"~", fmt.Sprintf("%d unchanged lines", gap)})
			if gap == 1 {
				out[len(out)-1].Text = "1 unchanged line"
			}
			gap = 0
		}
	}
	for i, o := range all {
		if !keep[i] {
			gap++
			continue
		}
		flush()
		switch o.kind {
		case "+":
			out = append(out, Line{"+", b.Lines[o.i]})
		default:
			out = append(out, Line{o.kind, a.Lines[o.i]})
		}
	}
	flush()
	return out, added, removed
}

type diffOp struct {
	kind string
	i    int
}

// diffOps is a line diff: common prefix and suffix, then an LCS of the middle
// (or, for very long middles, a cheaper set comparison).
func diffOps(a, b []string) []diffOp {
	var out []diffOp
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		out = append(out, diffOp{"=", pre})
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	am, bm := a[pre:len(a)-suf], b[pre:len(b)-suf]
	if len(am)*len(bm) <= 4_000_000 {
		out = append(out, lcs(am, bm, pre, pre)...)
	} else {
		out = append(out, setDiff(am, bm, pre, pre)...)
	}
	for k := 0; k < suf; k++ {
		out = append(out, diffOp{"=", len(a) - suf + k})
	}
	return out
}

func lcs(a, b []string, ao, bo int) []diffOp {
	n, m := len(a), len(b)
	// dp[i][j] = LCS of a[i:], b[j:]
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, diffOp{"=", ao + i})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, diffOp{"-", ao + i})
			i++
		default:
			out = append(out, diffOp{"+", bo + j})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, diffOp{"-", ao + i})
	}
	for ; j < m; j++ {
		out = append(out, diffOp{"+", bo + j})
	}
	return out
}

// setDiff: lines only in a are removed, lines only in b added (by count).
func setDiff(a, b []string, ao, bo int) []diffOp {
	inB := map[string]int{}
	for _, l := range b {
		inB[l]++
	}
	inA := map[string]int{}
	for _, l := range a {
		inA[l]++
	}
	var out []diffOp
	for i, l := range a {
		if inB[l] > 0 {
			inB[l]--
			out = append(out, diffOp{"=", ao + i})
		} else {
			out = append(out, diffOp{"-", ao + i})
		}
	}
	for j, l := range b {
		if inA[l] > 0 {
			inA[l]--
			continue
		}
		out = append(out, diffOp{"+", bo + j})
	}
	return out
}

// Changed returns the added and removed lines of a diff.
func Changed(d []Line) (added, removed []string) {
	for _, l := range d {
		switch l.Op {
		case "+":
			added = append(added, l.Text)
		case "-":
			removed = append(removed, l.Text)
		}
	}
	return
}

// AutoSummary describes a change without AI.
func AutoSummary(d []Line, added, removed int) string {
	a, r := Changed(d)
	first := func(ls []string) string {
		if len(ls) == 0 {
			return ""
		}
		return "“" + oneLine(ls[0], 90) + "”"
	}
	switch {
	case added == 1 && removed == 1:
		return fmt.Sprintf("%s → %s", first(r), first(a))
	case added > 0 && removed == 0:
		if added == 1 {
			return "New: " + first(a)
		}
		return fmt.Sprintf("%d new lines, starting %s", added, first(a))
	case removed > 0 && added == 0:
		if removed == 1 {
			return "Gone: " + first(r)
		}
		return fmt.Sprintf("%d lines gone, starting %s", removed, first(r))
	}
	return fmt.Sprintf("%d lines new, %d gone — now %s", added, removed, first(a))
}
