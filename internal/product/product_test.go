package product

import (
	"strings"
	"testing"
)

func TestParseJSONLD(t *testing.T) {
	page := `<html><head><title>ignored</title>
<script type="application/ld+json">{"@context":"https://schema.org","@graph":[{"@type":"WebPage"},{"@type":["Product"],"name":"Kettle &amp; Co",
"image":[{"@type":"ImageObject","url":"/k.jpg"}],"brand":{"@type":"Brand","name":"Acme"},
"offers":[{"@type":"Offer","price":"49.99","priceCurrency":"eur","availability":"http://schema.org/OutOfStock"},{"@type":"Offer","price":39.5,"priceCurrency":"EUR","availability":"InStock"}]}]}</script>
</head></html>`
	p := Parse(page, "https://www.shop.example/p/kettle")
	if p.Title != "Kettle & Co" || p.Price != "39.5" || p.Currency != "EUR" || p.Image != "https://www.shop.example/k.jpg" || p.Brand != "Acme" || p.Availability != "in_stock" || p.Store != "shop.example" {
		t.Fatalf("%+v", p)
	}
}

func TestParseMeta(t *testing.T) {
	page := `<html><head><meta property="og:title" content="Lamp"><meta content="https://cdn.example/l.png" property="og:image">
<meta property="product:price:amount" content="1.299,00"><meta property="product:price:currency" content="EUR">
<meta property="og:site_name" content="Light House"></head><body><span itemprop="availability" content="https://schema.org/PreOrder"></span></body></html>`
	p := Parse(page, "https://light.example/x")
	if p.Title != "Lamp" || p.Price != "1299.00" || p.Currency != "EUR" || p.Image != "https://cdn.example/l.png" || p.Store != "Light House" || p.Availability != "preorder" {
		t.Fatalf("%+v", p)
	}
}

func TestNormalizePrice(t *testing.T) {
	for in, want := range map[string]string{"12,99": "12.99", "1,299.00": "1299.00", "€ 1.299,50": "1299.50", "$1,299": "1299", "0,99": "0.99", "1299": "1299", "abc": "", "": "", "39.5": "39.5", "1.234.567": "1234567"} {
		if got := NormalizePrice(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	if ToMinor("12.5", 2) != 1250 || ToMinor("1299", 0) != 1299 || ToMinor("0", 2) != 0 {
		t.Fatal("ToMinor")
	}
}

func TestCheckURL(t *testing.T) {
	for _, bad := range []string{"javascript:alert(1)", "ftp://x", "notaurl", "file:///etc/passwd"} {
		if _, err := CheckURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestShipping(t *testing.T) {
	page := `<html><head><script type="application/ld+json">{"@type":"Product","name":"Lamp","offers":{"@type":"Offer","price":"40","priceCurrency":"EUR",
"shippingDetails":[{"@type":"OfferShippingDetails","shippingRate":{"value":"4.95","currency":"EUR"},"shippingDestination":{"@type":"DefinedRegion","addressCountry":"DE"}},
{"@type":"OfferShippingDetails","shippingRate":{"value":9.9,"currency":"EUR"},"shippingDestination":[{"addressCountry":"AT"},{"addressCountry":"CH"}]}]}}</script>
<script>var x = "versand nach überall";</script></head><body>
<p>Kostenloser Versand ab 50 € innerhalb Deutschlands.</p><div>Great lamp, bright light.</div>
<a href="/hilfe/versand-und-lieferung">Versand &amp; Lieferung</a> <a href="https://other.example/shipping">Shipping</a> <a href="/sendungsverfolgung">Sendungsverfolgung</a>
</body></html>`
	p := Parse(page, "https://www.lamps.example/p/1")
	if strings.Join(p.ShipsTo, ",") != "DE,AT,CH" || len(p.ShipRates) != 3 || p.ShipRates[1].Price != "9.9" || p.ShipRates[1].Country != "AT" {
		t.Fatalf("structured %+v %+v", p.ShipsTo, p.ShipRates)
	}
	if len(p.ShipText) != 1 || !strings.Contains(p.ShipText[0], "innerhalb Deutschlands") {
		t.Fatalf("text %q", p.ShipText)
	}
	if len(p.ShipLinks) != 1 || p.ShipLinks[0] != "https://www.lamps.example/hilfe/versand-und-lieferung" {
		t.Fatalf("links %q", p.ShipLinks)
	}
}
