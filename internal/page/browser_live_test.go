package page

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestBrowserLive(t *testing.T) {
	u := os.Getenv("LIVE_BROWSER")
	if u == "" {
		t.Skip()
	}
	b := NewBrowser(u, 60*time.Second, 1)
	res, err := b.Get(context.Background(), "https://example.com/", nil, time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	ext, err := Extract(res, "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("status %d final %s shot %d bytes text %q", res.Status, res.FinalURL, len(res.Shot), ext.Text)
}
