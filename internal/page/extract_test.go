package page

import (
	"strings"
	"testing"
)

func TestExtractBlocksAndTables(t *testing.T) {
	html := `<html><head><title>T</title><style>.x{}</style></head><body><div><h1>Example Domain</h1><p>Hello <b>world</b></p>
	<table><tr><th>Date</th><th>Area</th></tr><tr><td>05.11.</td><td>Dobrinja</td></tr></table><ul><li>one</li><li>two</li></ul>
	<script>var a=1</script><div hidden>secret</div></div></body></html>`
	ext, err := Extract(&Result{Body: []byte(html), ContentType: "text/html"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := "Example Domain\nHello world\nDate | Area\n05.11. | Dobrinja\n• one\n• two"
	if ext.Text != want || ext.Title != "T" {
		t.Fatalf("got %q (title %q)", ext.Text, ext.Title)
	}
	if strings.Contains(ext.Text, "secret") || strings.Contains(ext.Text, "var a") {
		t.Fatal("hidden or script text leaked")
	}
}

func TestNoiseKeyAndRules(t *testing.T) {
	cases := [][2]string{
		{"Updated 10:42 · 1,204 views", "Updated 11:05 · 1,388 views"},
		{"Posted 3 minutes ago", "Posted 25 minutes ago"},
		{"Stand: 20.09.2026", "Stand: 01.11.2026"},
		{"Last change 2026-10-30T12:00:00Z", "Last change 2026-11-01T08:00:00Z"},
		{"Nov 1, 2026 release", "Oct 30, 2026 release"},
	}
	for _, c := range cases {
		if NoiseKey(c[0]) != NoiseKey(c[1]) {
			t.Errorf("%q vs %q: %q != %q", c[0], c[1], NoiseKey(c[0]), NoiseKey(c[1]))
		}
	}
	if NoiseKey("Price: 49 €") == NoiseKey("Price: 39 €") {
		t.Error("prices must not be noise")
	}
	if !Matches("3 slots AVAILABLE", "available") || !Matches("Price 39", `/price \d+/`) || Matches("x", "/[/") {
		t.Error("rules")
	}
	if ValidRule("/[/") == "" {
		t.Error("invalid regex accepted")
	}
}

func TestDiffContext(t *testing.T) {
	a := Prepare(strings.Join([]string{"a", "b", "c", "d", "e", "f", "g", "h"}, "\n"), nil, false)
	b := Prepare(strings.Join([]string{"a", "b", "c", "d", "E", "f", "g", "h", "i"}, "\n"), nil, false)
	d, added, removed := Diff(a, b, 1)
	if added != 2 || removed != 1 {
		t.Fatalf("added %d removed %d", added, removed)
	}
	var ops []string
	for _, l := range d {
		ops = append(ops, l.Op+l.Text)
	}
	got := strings.Join(ops, ",")
	if got != "~3 unchanged lines,=d,-e,+E,=f,~1 unchanged line,=h,+i" {
		t.Fatalf("diff = %s", got)
	}
	if s := AutoSummary(d, added, removed); s == "" {
		t.Fatal("no summary")
	}
}

func TestJSONPathAndPretty(t *testing.T) {
	r := &Result{Body: []byte(`{"b": 2, "a": {"list": [{"v": "x"}, {"v": "y"}]}}`), ContentType: "application/json"}
	ext, err := Extract(r, "$.a.list[1].v", "")
	if err != nil || ext.Text != "y" {
		t.Fatalf("%q %v", ext, err)
	}
	if _, err := Extract(r, "$.a.nope", ""); err == nil {
		t.Fatal("missing path should fail")
	}
	whole, _ := Extract(r, "", "")
	if !strings.HasPrefix(whole.Text, "{\n  \"a\"") {
		t.Fatalf("keys not sorted: %q", whole.Text)
	}
}
