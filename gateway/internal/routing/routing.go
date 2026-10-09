// Package routing decides, in memory, what a message costs the client, which vendor connections it is sent
// to (in failover order), and how content rules change it. A Snapshot is immutable; the engine builds a new
// one whenever configuration changes and swaps it in atomically, so changes apply without restarts.
package routing

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Micros is an amount of money in millionths (NUMERIC(18,6) in the database).
type Micros int64

// ParseMicros parses a decimal string such as "0.0125".
func ParseMicros(s string) (Micros, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 6 {
		frac = frac[:6]
	}
	frac += strings.Repeat("0", 6-len(frac))
	if whole == "" {
		whole = "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	v := Micros(w*1_000_000 + f)
	if neg {
		v = -v
	}
	return v, nil
}

// String formats the amount with 6 decimals, suitable for NUMERIC parameters.
func (m Micros) String() string {
	sign := ""
	if m < 0 {
		sign, m = "-", -m
	}
	return fmt.Sprintf("%s%d.%06d", sign, int64(m)/1_000_000, int64(m)%1_000_000)
}

type Network struct {
	ID         int64
	CountryISO string
	MCC, MNC   string
	Name       string
}

type Connection struct {
	ID          int64
	VendorID    int64
	Name        string
	Enabled     bool
	MaxAttempts int
}

type Target struct {
	ConnectionID int64
	Position     int
	Weight       int
}

type Route struct {
	ID            int64
	Name          string
	Priority      int
	ClientID      int64 // 0 = any
	AccountID     int64 // 0 = any
	CountryISO    string
	NetworkID     int64
	SenderMatch   string // any, exact, prefix, regex
	SenderPattern string
	Policy        string // priority, weighted, lcr
	AllowLoss     bool
	Targets       []Target
	senderRe      *regexp.Regexp
}

type ContentRule struct {
	ID            int64
	Name          string
	Priority      int
	ClientID      int64
	CountryISO    string
	SenderPattern string
	TextPattern   string
	Action        string // replace_text, replace_sender, prepend, append, block
	Find          string
	ReplaceWith   string
	senderRe      *regexp.Regexp
	textRe        *regexp.Regexp
	findRe        *regexp.Regexp
}

// rateKey identifies a price: owner (client or connection), country and network (0 = whole country).
type rateKey struct {
	owner     int64
	country   string
	networkID int64
}

// Snapshot holds everything needed to route and price a message.
type Snapshot struct {
	prefixes     map[string]int64 // number prefix -> network id
	maxPrefixLen int
	networks     map[int64]Network
	dialCodes    map[string]string // dial code -> country, for numbers without a network prefix match
	maxDialLen   int
	clientRates  map[rateKey]Micros
	vendorRates  map[rateKey]Micros
	connections  map[int64]Connection
	routes       []*Route
	rules        []*ContentRule
	blacklist    map[int64]map[string]struct{} // client id (0 = everyone) -> numbers
	moRoutes     []*MORoute
}

// MORoute sends incoming messages for a number (and optional keyword) to a client.
type MORoute struct {
	ID           int64
	Name         string
	Priority     int
	NumberPrefix string
	Keyword      string // upper-case
	ClientID     int64
	AccountID    int64
	AutoOptOut   bool
}

// Builder collects configuration and produces a Snapshot.
type Builder struct{ s *Snapshot }

func NewBuilder() *Builder {
	return &Builder{s: &Snapshot{
		prefixes: map[string]int64{}, networks: map[int64]Network{}, dialCodes: map[string]string{},
		clientRates: map[rateKey]Micros{}, vendorRates: map[rateKey]Micros{}, connections: map[int64]Connection{},
		blacklist: map[int64]map[string]struct{}{},
	}}
}

func (b *Builder) Network(n Network) { b.s.networks[n.ID] = n }

func (b *Builder) Prefix(prefix string, networkID int64) {
	b.s.prefixes[prefix] = networkID
	if len(prefix) > b.s.maxPrefixLen {
		b.s.maxPrefixLen = len(prefix)
	}
}

func (b *Builder) DialCode(code, countryISO string) {
	if code == "" {
		return
	}
	b.s.dialCodes[code] = countryISO
	if len(code) > b.s.maxDialLen {
		b.s.maxDialLen = len(code)
	}
}

// ClientRate sets the current price for a client; networkID 0 means the whole country.
func (b *Builder) ClientRate(clientID int64, country string, networkID int64, price Micros) {
	b.s.clientRates[rateKey{clientID, strings.ToUpper(country), networkID}] = price
}

func (b *Builder) VendorRate(connectionID int64, country string, networkID int64, price Micros) {
	b.s.vendorRates[rateKey{connectionID, strings.ToUpper(country), networkID}] = price
}

func (b *Builder) Connection(c Connection) { b.s.connections[c.ID] = c }

// Blacklist blocks a number for one client (clientID 0 = for every client).
func (b *Builder) Blacklist(clientID int64, number string) {
	n := NormalizeNumber(number)
	if n == "" {
		return
	}
	set := b.s.blacklist[clientID]
	if set == nil {
		set = map[string]struct{}{}
		b.s.blacklist[clientID] = set
	}
	set[n] = struct{}{}
}

func (b *Builder) MORoute(r MORoute) {
	r.Keyword = strings.ToUpper(strings.TrimSpace(r.Keyword))
	r.NumberPrefix = NormalizeNumber(r.NumberPrefix)
	b.s.moRoutes = append(b.s.moRoutes, &r)
}

// Blacklisted reports whether a normalized number is blocked for the client.
func (s *Snapshot) Blacklisted(clientID int64, number string) bool {
	if _, ok := s.blacklist[0][number]; ok {
		return true
	}
	_, ok := s.blacklist[clientID][number]
	return ok
}

// OptOutWords are replies that unsubscribe the sender.
var OptOutWords = map[string]bool{"STOP": true, "UNSUBSCRIBE": true, "STOPALL": true, "END": true, "CANCEL": true, "OPTOUT": true}

// FirstWord returns the upper-cased first word of a text.
func FirstWord(text string) string {
	f := strings.Fields(text)
	if len(f) == 0 {
		return ""
	}
	return strings.ToUpper(strings.Trim(f[0], ".,!?"))
}

// MatchMO finds the route for an incoming message sent to `to` with `text`. Routes with a keyword win over
// routes without one; then the longest number prefix; then priority.
func (s *Snapshot) MatchMO(to, text string) *MORoute {
	to = NormalizeNumber(to)
	word := FirstWord(text)
	var best *MORoute
	score := func(r *MORoute) int {
		n := len(r.NumberPrefix) * 2
		if r.Keyword != "" {
			n += 1000
		}
		return n
	}
	for _, r := range s.moRoutes {
		if r.NumberPrefix != "" && !strings.HasPrefix(to, r.NumberPrefix) {
			continue
		}
		if r.Keyword != "" && r.Keyword != word {
			continue
		}
		if best == nil || score(r) > score(best) || (score(r) == score(best) && r.Priority < best.Priority) {
			best = r
		}
	}
	return best
}

// Route adds a route. Regex patterns are compiled here; an invalid pattern is an error.
func (b *Builder) Route(r Route) error {
	r.CountryISO = strings.ToUpper(r.CountryISO)
	if r.SenderMatch == "regex" {
		re, err := regexp.Compile(r.SenderPattern)
		if err != nil {
			return fmt.Errorf("route %q: invalid sender pattern: %w", r.Name, err)
		}
		r.senderRe = re
	}
	b.s.routes = append(b.s.routes, &r)
	return nil
}

func (b *Builder) ContentRule(c ContentRule) error {
	c.CountryISO = strings.ToUpper(c.CountryISO)
	var err error
	if c.SenderPattern != "" {
		if c.senderRe, err = regexp.Compile(c.SenderPattern); err != nil {
			return fmt.Errorf("content rule %q: invalid sender pattern: %w", c.Name, err)
		}
	}
	if c.TextPattern != "" {
		if c.textRe, err = regexp.Compile(c.TextPattern); err != nil {
			return fmt.Errorf("content rule %q: invalid text pattern: %w", c.Name, err)
		}
	}
	if c.Action == "replace_text" {
		if c.findRe, err = regexp.Compile(c.Find); err != nil {
			return fmt.Errorf("content rule %q: invalid find pattern: %w", c.Name, err)
		}
	}
	b.s.rules = append(b.s.rules, &c)
	return nil
}

// Build sorts routes (priority, then most specific first) and rules, and returns the snapshot.
func (b *Builder) Build() *Snapshot {
	s := b.s
	sort.SliceStable(s.routes, func(i, j int) bool {
		a, c := s.routes[i], s.routes[j]
		if a.Priority != c.Priority {
			return a.Priority < c.Priority
		}
		return specificity(a) > specificity(c)
	})
	sort.SliceStable(s.rules, func(i, j int) bool { return s.rules[i].Priority < s.rules[j].Priority })
	b.s = nil
	return s
}

func specificity(r *Route) int {
	n := 0
	if r.AccountID != 0 {
		n += 16
	}
	if r.ClientID != 0 {
		n += 8
	}
	if r.NetworkID != 0 {
		n += 4
	}
	if r.CountryISO != "" {
		n += 2
	}
	if r.SenderMatch != "any" && r.SenderMatch != "" {
		n++
	}
	return n
}

// Destination is the result of a number lookup.
type Destination struct {
	Number     string
	CountryISO string
	NetworkID  int64
}

// NormalizeNumber keeps digits only and removes a leading 00 or +.
func NormalizeNumber(n string) string {
	var b strings.Builder
	for _, r := range n {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return strings.TrimPrefix(b.String(), "00")
}

// Lookup finds the network (longest prefix) and country of a number.
func (s *Snapshot) Lookup(number string) Destination {
	d := Destination{Number: NormalizeNumber(number)}
	for l := min(len(d.Number), s.maxPrefixLen); l > 0; l-- {
		if id, ok := s.prefixes[d.Number[:l]]; ok {
			d.NetworkID = id
			d.CountryISO = s.networks[id].CountryISO
			return d
		}
	}
	for l := min(len(d.Number), s.maxDialLen); l > 0; l-- {
		if iso, ok := s.dialCodes[d.Number[:l]]; ok {
			d.CountryISO = iso
			return d
		}
	}
	return d
}

func (s *Snapshot) Network(id int64) (Network, bool) {
	n, ok := s.networks[id]
	return n, ok
}

// ClientPrice returns the client's price per part for the destination (network rate, else country rate).
func (s *Snapshot) ClientPrice(clientID int64, d Destination) (Micros, bool) {
	return lookupRate(s.clientRates, clientID, d)
}

// VendorCost returns a connection's cost per part for the destination.
func (s *Snapshot) VendorCost(connectionID int64, d Destination) (Micros, bool) {
	return lookupRate(s.vendorRates, connectionID, d)
}

func lookupRate(m map[rateKey]Micros, owner int64, d Destination) (Micros, bool) {
	if d.NetworkID != 0 {
		if p, ok := m[rateKey{owner, d.CountryISO, d.NetworkID}]; ok {
			return p, true
		}
	}
	p, ok := m[rateKey{owner, d.CountryISO, 0}]
	return p, ok
}

func (s *Snapshot) Connection(id int64) (Connection, bool) {
	c, ok := s.connections[id]
	return c, ok
}

// Request describes the message being routed.
type Request struct {
	ClientID  int64
	AccountID int64
	Sender    string
	Dest      Destination
	Price     Micros // client price per part
}

// Plan is the routing decision: connections in the order they should be tried.
type Plan struct {
	RouteID     int64
	RouteName   string
	Connections []int64
}

var ErrNoRoute = errors.New("no route for destination")

// Select picks the first matching route and orders its usable connections by the route's policy.
// Connections that are disabled, or that would cost more than the client pays (unless the route allows a
// loss), are left out. LCR only uses connections that have a rate for the destination.
func (s *Snapshot) Select(req Request) (Plan, error) {
	for _, r := range s.routes {
		if !r.matches(req) {
			continue
		}
		type cand struct {
			id     int64
			cost   Micros
			known  bool
			pos    int
			weight int
		}
		var cands []cand
		for _, t := range r.Targets {
			c, ok := s.connections[t.ConnectionID]
			if !ok || !c.Enabled {
				continue
			}
			cost, known := s.VendorCost(t.ConnectionID, req.Dest)
			if known && cost > req.Price && !r.AllowLoss {
				continue
			}
			if r.Policy == "lcr" && !known {
				continue
			}
			cands = append(cands, cand{t.ConnectionID, cost, known, t.Position, t.Weight})
		}
		if len(cands) == 0 {
			continue // try the next matching route
		}
		switch r.Policy {
		case "lcr":
			sort.SliceStable(cands, func(i, j int) bool {
				if cands[i].cost != cands[j].cost {
					return cands[i].cost < cands[j].cost
				}
				return cands[i].pos < cands[j].pos
			})
		case "weighted":
			// Weighted random order: pick one by weight, then the rest by weight.
			total := 0
			for _, c := range cands {
				total += c.weight
			}
			sort.SliceStable(cands, func(i, j int) bool { return cands[i].weight > cands[j].weight })
			if total > 0 {
				x := rand.IntN(total)
				for i, c := range cands {
					if x < c.weight {
						first := cands[i]
						cands = append([]cand{first}, append(cands[:i:i], cands[i+1:]...)...)
						break
					}
					x -= c.weight
				}
			}
		default:
			sort.SliceStable(cands, func(i, j int) bool { return cands[i].pos < cands[j].pos })
		}
		p := Plan{RouteID: r.ID, RouteName: r.Name}
		for _, c := range cands {
			p.Connections = append(p.Connections, c.id)
		}
		return p, nil
	}
	return Plan{}, ErrNoRoute
}

func (r *Route) matches(req Request) bool {
	if r.ClientID != 0 && r.ClientID != req.ClientID {
		return false
	}
	if r.AccountID != 0 && r.AccountID != req.AccountID {
		return false
	}
	if r.CountryISO != "" && r.CountryISO != req.Dest.CountryISO {
		return false
	}
	if r.NetworkID != 0 && r.NetworkID != req.Dest.NetworkID {
		return false
	}
	switch r.SenderMatch {
	case "exact":
		return strings.EqualFold(req.Sender, r.SenderPattern)
	case "prefix":
		return strings.HasPrefix(strings.ToLower(req.Sender), strings.ToLower(r.SenderPattern))
	case "regex":
		return r.senderRe.MatchString(req.Sender)
	}
	return true
}

// Content is the message content that rules may change.
type Content struct {
	Sender string
	Text   string
}

// ErrBlocked is returned when a content rule blocks the message.
type ErrBlocked struct{ Rule string }

func (e *ErrBlocked) Error() string { return "blocked by content rule " + e.Rule }

// ApplyContent runs the matching content rules in priority order and returns the changed content and the
// names of the rules that changed it.
func (s *Snapshot) ApplyContent(clientID int64, country string, c Content) (Content, []string, error) {
	var applied []string
	for _, r := range s.rules {
		if r.ClientID != 0 && r.ClientID != clientID {
			continue
		}
		if r.CountryISO != "" && r.CountryISO != country {
			continue
		}
		if r.senderRe != nil && !r.senderRe.MatchString(c.Sender) {
			continue
		}
		if r.textRe != nil && !r.textRe.MatchString(c.Text) {
			continue
		}
		before := c
		switch r.Action {
		case "block":
			return c, append(applied, r.Name), &ErrBlocked{Rule: r.Name}
		case "replace_text":
			c.Text = r.findRe.ReplaceAllString(c.Text, r.ReplaceWith)
		case "replace_sender":
			c.Sender = r.ReplaceWith
		case "prepend":
			c.Text = r.ReplaceWith + c.Text
		case "append":
			c.Text += r.ReplaceWith
		}
		if c != before {
			applied = append(applied, r.Name)
		}
	}
	return c, applied, nil
}
