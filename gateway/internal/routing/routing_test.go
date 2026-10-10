package routing

import (
	"errors"
	"testing"
)

func m(s string) Micros { v, _ := ParseMicros(s); return v }

// fixture: Pakistan with two networks; three vendor connections with different costs.
func fixture(t *testing.T, routes ...Route) *Snapshot {
	t.Helper()
	return fixtureWithQuality(t, nil, routes...)
}

func fixtureWithQuality(t *testing.T, quality []Quality, routes ...Route) *Snapshot {
	t.Helper()
	b := NewBuilder()
	for _, q := range quality {
		b.Quality(q)
	}
	b.Network(Network{ID: 1, CountryISO: "PK", MCC: "410", MNC: "01", Name: "Jazz"})
	b.Network(Network{ID: 2, CountryISO: "PK", MCC: "410", MNC: "06", Name: "Telenor"})
	b.Prefix("92300", 1)
	b.Prefix("92345", 2)
	b.DialCode("92", "PK")
	b.DialCode("1", "US")
	for _, id := range []int64{10, 20, 30} {
		b.Connection(Connection{ID: id, Enabled: true, MaxAttempts: 2})
	}
	b.Connection(Connection{ID: 40, Enabled: false})
	b.ClientRate(7, "PK", 0, m("0.0100"))
	b.ClientRate(7, "PK", 2, m("0.0200"))
	b.VendorRate(10, "PK", 0, m("0.0090"))
	b.VendorRate(20, "PK", 0, m("0.0050"))
	b.VendorRate(30, "PK", 0, m("0.0150")) // more than the client pays for network 1
	for _, r := range routes {
		if err := b.Route(r); err != nil {
			t.Fatal(err)
		}
	}
	return b.Build()
}

func targets(ids ...int64) []Target {
	var ts []Target
	for i, id := range ids {
		ts = append(ts, Target{ConnectionID: id, Position: i, Weight: 100})
	}
	return ts
}

func TestMicros(t *testing.T) {
	if m("0.0125").String() != "0.012500" || m("-3").String() != "-3.000000" || m("12.3456789").String() != "12.345678" {
		t.Fatal("micros parse/format mismatch")
	}
}

func TestLookupLongestPrefixThenDialCode(t *testing.T) {
	s := fixture(t)
	if d := s.Lookup("+92 300 1234567"); d.NetworkID != 1 || d.CountryISO != "PK" || d.Number != "923001234567" {
		t.Fatalf("got %+v", d)
	}
	if d := s.Lookup("00923459876543"); d.NetworkID != 2 {
		t.Fatalf("got %+v", d)
	}
	if d := s.Lookup("923331234567"); d.NetworkID != 0 || d.CountryISO != "PK" {
		t.Fatalf("unknown network should fall back to country: %+v", d)
	}
}

func TestClientPriceNetworkOverridesCountry(t *testing.T) {
	s := fixture(t)
	if p, _ := s.ClientPrice(7, s.Lookup("923001234567")); p != m("0.01") {
		t.Fatalf("country rate expected, got %s", p)
	}
	if p, _ := s.ClientPrice(7, s.Lookup("923451234567")); p != m("0.02") {
		t.Fatalf("network rate expected, got %s", p)
	}
	if _, ok := s.ClientPrice(8, s.Lookup("923001234567")); ok {
		t.Fatal("client without rates must not get a price")
	}
}

func TestPriorityRouteFailoverOrderSkipsLossAndDisabled(t *testing.T) {
	s := fixture(t, Route{ID: 1, Name: "pk", Priority: 10, CountryISO: "PK", Policy: "priority", Targets: targets(30, 40, 10, 20)})
	d := s.Lookup("923001234567")
	p, err := s.Select(Request{ClientID: 7, Dest: d, Price: m("0.01")})
	if err != nil {
		t.Fatal(err)
	}
	// 30 costs more than the price, 40 is disabled
	if len(p.Connections) != 2 || p.Connections[0] != 10 || p.Connections[1] != 20 {
		t.Fatalf("got %v", p.Connections)
	}
}

func TestLCROrdersByCost(t *testing.T) {
	s := fixture(t, Route{ID: 1, Name: "lcr", CountryISO: "PK", Policy: "lcr", AllowLoss: true, Targets: targets(30, 10, 20)})
	p, err := s.Select(Request{ClientID: 7, Dest: s.Lookup("923001234567"), Price: m("0.01")})
	if err != nil {
		t.Fatal(err)
	}
	if p.Connections[0] != 20 || p.Connections[1] != 10 || p.Connections[2] != 30 {
		t.Fatalf("got %v", p.Connections)
	}
}

func TestSenderRouteWinsWhenMoreSpecific(t *testing.T) {
	s := fixture(t,
		Route{ID: 1, Name: "default", Priority: 100, Policy: "priority", Targets: targets(10)},
		Route{ID: 2, Name: "bank sender", Priority: 100, SenderMatch: "regex", SenderPattern: "(?i)^bank", Policy: "priority", Targets: targets(20)},
	)
	d := s.Lookup("923001234567")
	if p, _ := s.Select(Request{ClientID: 7, Sender: "BANKALERT", Dest: d, Price: m("0.01")}); p.RouteID != 2 {
		t.Fatalf("sender route expected, got %+v", p)
	}
	if p, _ := s.Select(Request{ClientID: 7, Sender: "SHOP", Dest: d, Price: m("0.01")}); p.RouteID != 1 {
		t.Fatalf("default route expected, got %+v", p)
	}
}

func TestClientRouteAndFallThrough(t *testing.T) {
	s := fixture(t,
		Route{ID: 1, Name: "client 7 only loss", Priority: 10, ClientID: 7, Policy: "priority", Targets: targets(30)},
		Route{ID: 2, Name: "fallback", Priority: 20, Policy: "priority", Targets: targets(10)},
	)
	// route 1 matches but its only target is too expensive, so route 2 is used
	p, err := s.Select(Request{ClientID: 7, Dest: s.Lookup("923001234567"), Price: m("0.01")})
	if err != nil || p.RouteID != 2 {
		t.Fatalf("got %+v %v", p, err)
	}
	if _, err := fixture(t).Select(Request{ClientID: 7, Dest: s.Lookup("1555"), Price: 1}); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("want ErrNoRoute, got %v", err)
	}
}

func TestWeightedUsesAllTargets(t *testing.T) {
	s := fixture(t, Route{ID: 1, CountryISO: "PK", Policy: "weighted", AllowLoss: true,
		Targets: []Target{{ConnectionID: 10, Weight: 50}, {ConnectionID: 20, Weight: 50}}})
	seen := map[int64]int{}
	for i := 0; i < 400; i++ {
		p, _ := s.Select(Request{ClientID: 7, Dest: s.Lookup("923001234567"), Price: m("0.01")})
		seen[p.Connections[0]]++
		if len(p.Connections) != 2 {
			t.Fatal("failover target missing")
		}
	}
	if seen[10] < 120 || seen[20] < 120 {
		t.Fatalf("split too uneven: %v", seen)
	}
}

func TestContentRules(t *testing.T) {
	b := NewBuilder()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(b.ContentRule(ContentRule{Name: "mask link", Priority: 1, Action: "replace_text", Find: `https?://\S+`, ReplaceWith: "[link]"}))
	must(b.ContentRule(ContentRule{Name: "sender", Priority: 2, ClientID: 7, Action: "replace_sender", ReplaceWith: "TELIVOZ"}))
	must(b.ContentRule(ContentRule{Name: "no casino", Priority: 3, TextPattern: "(?i)casino", Action: "block"}))
	s := b.Build()

	c, applied, err := s.ApplyContent(7, "PK", Content{Sender: "X", Text: "visit https://a.b now"})
	if err != nil || c.Text != "visit [link] now" || c.Sender != "TELIVOZ" || len(applied) != 2 {
		t.Fatalf("got %+v %v %v", c, applied, err)
	}
	if _, _, err := s.ApplyContent(9, "PK", Content{Text: "Casino bonus"}); err == nil {
		t.Fatal("expected block")
	}
}

func TestBlacklistGlobalAndPerClient(t *testing.T) {
	b := NewBuilder()
	b.Blacklist(0, "+92 300 0000001")
	b.Blacklist(7, "923000000002")
	s := b.Build()
	if !s.Blacklisted(7, "923000000001") || !s.Blacklisted(9, "923000000001") {
		t.Fatal("global entry must block every client")
	}
	if !s.Blacklisted(7, "923000000002") || s.Blacklisted(9, "923000000002") {
		t.Fatal("client entry must block only that client")
	}
}

func TestMatchMO(t *testing.T) {
	b := NewBuilder()
	b.MORoute(MORoute{ID: 1, Name: "any on 8899", NumberPrefix: "8899", ClientID: 1})
	b.MORoute(MORoute{ID: 2, Name: "PROMO on 8899", NumberPrefix: "8899", Keyword: "promo", ClientID: 2})
	b.MORoute(MORoute{ID: 3, Name: "long number", NumberPrefix: "92300555", ClientID: 3})
	s := b.Build()
	if r := s.MatchMO("8899", "promo please"); r == nil || r.ID != 2 {
		t.Fatalf("keyword route expected, got %+v", r)
	}
	if r := s.MatchMO("8899", "hello"); r == nil || r.ID != 1 {
		t.Fatalf("number route expected, got %+v", r)
	}
	if r := s.MatchMO("+92300555123", "hi"); r == nil || r.ID != 3 {
		t.Fatalf("long number route expected, got %+v", r)
	}
	if r := s.MatchMO("1234", "hi"); r != nil {
		t.Fatalf("no route expected, got %+v", r)
	}
	if FirstWord("  Stop. ") != "STOP" {
		t.Fatal("first word")
	}
}

func TestQualityScore(t *testing.T) {
	if got := QualityScore(5, 10, 0); got != neutralScore {
		t.Fatalf("too few samples: %v", got)
	}
	if got := QualityScore(90, 100, 0); got != 90 {
		t.Fatalf("90%% delivered: %v", got)
	}
	if got := QualityScore(90, 100, 30_000); got != 85 { // 30 s average DLR time costs 5 points
		t.Fatalf("slow DLRs: %v", got)
	}
	if got := QualityScore(100, 100, 600_000); got != 90 { // the latency penalty is capped at 10
		t.Fatalf("penalty cap: %v", got)
	}
}

func TestQualityAndBalancedPolicies(t *testing.T) {
	// Connection 10 (0.0090) delivers 95%, 20 (0.0050) 93%, 30 (no affordable rate, loss allowed) 99%.
	q := []Quality{
		{ConnectionID: 10, CountryISO: "PK", Delivered: 950, Final: 1000},
		{ConnectionID: 20, CountryISO: "PK", Delivered: 930, Final: 1000},
		{ConnectionID: 30, CountryISO: "PK", Delivered: 990, Final: 1000},
	}
	req := func(s *Snapshot) Request {
		return Request{ClientID: 7, Dest: s.Lookup("923001234567"), Price: m("0.01")}
	}

	s := fixtureWithQuality(t, q, Route{ID: 1, Name: "q", CountryISO: "PK", Policy: "quality", AllowLoss: true, Targets: targets(20, 10, 30)})
	if p, err := s.Select(req(s)); err != nil || p.Connections[0] != 30 || p.Connections[1] != 10 || p.Connections[2] != 20 {
		t.Fatalf("quality: %v %v", p.Connections, err)
	}

	// Balanced: 10 and 20 are within 5 points of the best (99)? 95 yes, 93 no. So 10 is cheapest among
	// 30 and 10 (30 costs more), then 30, then 20.
	s = fixtureWithQuality(t, q, Route{ID: 1, Name: "b", CountryISO: "PK", Policy: "balanced", AllowLoss: true, Targets: targets(20, 10, 30)})
	if p, err := s.Select(req(s)); err != nil || p.Connections[0] != 10 || p.Connections[1] != 30 || p.Connections[2] != 20 {
		t.Fatalf("balanced: %v %v", p.Connections, err)
	}

	// A connection with too little traffic gets a neutral score and still appears.
	s = fixtureWithQuality(t, q[:1], Route{ID: 1, Name: "q", CountryISO: "PK", Policy: "quality", Targets: targets(20, 10)})
	if p, err := s.Select(req(s)); err != nil || p.Connections[0] != 10 || len(p.Connections) != 2 {
		t.Fatalf("new vendor: %v %v", p.Connections, err)
	}
}
