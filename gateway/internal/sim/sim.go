// Package sim provides SMPP simulators: a vendor (SMSC) that accepts messages and returns DLRs, and a client
// (ESME) that sends traffic and counts DLRs. Used by tests and by `smppsim` for load tests.
package sim

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/smpp"
)

// VendorMode controls how the vendor answers submit_sm.
type VendorMode string

const (
	VendorOK       VendorMode = "ok"       // accept and send a DLR
	VendorReject   VendorMode = "reject"   // answer with an error status
	VendorThrottle VendorMode = "throttle" // answer "throttled" to the first N submits, then accept
	VendorNoAck    VendorMode = "noack"    // never answer (the "resend 1000 times" scenario)
)

// Vendor is a fake SMSC.
type Vendor struct {
	Mode      VendorMode
	FailRatio float64       // share of accepted messages that get an UNDELIV DLR
	DLRDelay  time.Duration // delay before the DLR
	ThrottleN int64
	HexIDs    bool // answer submit_sm_resp with hex ids but send DLR ids in decimal (a common vendor quirk)
	ln        net.Listener
	nextID    atomic.Int64
	Received  atomic.Int64
	throttled atomic.Int64
	mu        sync.Mutex
	perDest   map[string]int
	sessions  []*smpp.Session
}

// Start listens on a random local port (or addr) and returns the address.
func (v *Vendor) Start(addr string) (string, error) {
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	v.ln = ln
	v.perDest = map[string]int{}
	v.nextID.Store(1000)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s := smpp.NewSession(conn, smpp.SessionOptions{Handler: v.handle, MaxInflight: 256})
			v.mu.Lock()
			v.sessions = append(v.sessions, s)
			v.mu.Unlock()
			go s.Serve()
		}
	}()
	return ln.Addr().String(), nil
}

func (v *Vendor) Close() {
	if v.ln != nil {
		v.ln.Close()
	}
	v.mu.Lock()
	for _, s := range v.sessions {
		s.Close()
	}
	v.mu.Unlock()
}

// CountFor returns how many submit_sm the vendor received for a destination.
// ResetIDs restarts the vendor's message ID counter, as an SMSC does after a restart.
func (v *Vendor) ResetIDs() { v.nextID.Store(1000) }

func (v *Vendor) CountFor(dest string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.perDest[dest]
}

func (v *Vendor) handle(s *smpp.Session, p *smpp.PDU) {
	switch p.CommandID {
	case smpp.BindTransceiver, smpp.BindTransmitter, smpp.BindReceiver:
		s.BindCommand = p.CommandID
		_ = s.Respond(p, smpp.StatusOK, &smpp.BindResp{SystemID: "vendor-sim"})
	case smpp.SubmitSm:
		sm := p.Body.(*smpp.Sm)
		v.Received.Add(1)
		v.mu.Lock()
		v.perDest[sm.Destination]++
		v.mu.Unlock()
		switch v.Mode {
		case VendorNoAck:
			return
		case VendorReject:
			_ = s.Respond(p, smpp.StatusSubmitFail, &smpp.MessageIDResp{})
			return
		case VendorThrottle:
			if v.throttled.Add(1) <= v.ThrottleN {
				_ = s.Respond(p, smpp.StatusThrottled, &smpp.MessageIDResp{})
				return
			}
		}
		n := v.nextID.Add(1)
		respID, dlrID := fmt.Sprint(n), fmt.Sprint(n)
		if v.HexIDs {
			respID = fmt.Sprintf("%x", n)
		}
		_ = s.Respond(p, smpp.StatusOK, &smpp.MessageIDResp{MessageID: respID})
		if sm.RegisteredDelivery&0x03 == 0 {
			return
		}
		stat := "DELIVRD"
		if v.FailRatio > 0 && rand.Float64() < v.FailRatio {
			stat = "UNDELIV"
		}
		go func() {
			time.Sleep(v.DLRDelay)
			now := time.Now()
			r := &smpp.Receipt{ID: dlrID, SubmitAt: now, DoneAt: now, Stat: stat, Err: "000"}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			body := r.Sm(sm.Destination, sm.Source, 1, 1, sm.SourceTon, sm.SourceNpi)
			// Like real SMSCs, keep the DLR until some bind can take it (the gateway may have reconnected).
			for i := 0; i < 50; i++ {
				target := v.liveSession(s)
				if target != nil {
					if _, err := target.Request(ctx, smpp.DeliverSm, body); err == nil {
						return
					}
				}
				time.Sleep(200 * time.Millisecond)
			}
		}()
	default:
		_ = s.Nack(p, smpp.StatusInvCmdID)
	}
}

// SendMO delivers an incoming (subscriber) message to the gateway over any bound session.
func (v *Vendor) SendMO(ctx context.Context, from, to, text string) error {
	s := v.liveSession(nil)
	if s == nil {
		return fmt.Errorf("no bound session")
	}
	_, err := s.Request(ctx, smpp.DeliverSm, &smpp.Sm{SourceTon: 1, SourceNpi: 1, Source: from, DestTon: 1, DestNpi: 1,
		Destination: to, ShortMessage: []byte(text)})
	return err
}

// liveSession returns preferred if it is still open, otherwise any open session that can receive.
func (v *Vendor) liveSession(preferred *smpp.Session) *smpp.Session {
	if preferred != nil {
		select {
		case <-preferred.Done():
		default:
			return preferred
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for i := len(v.sessions) - 1; i >= 0; i-- {
		s := v.sessions[i]
		select {
		case <-s.Done():
			continue
		default:
		}
		if s.CanReceive() {
			return s
		}
	}
	return nil
}

// Client is a fake ESME that sends traffic and counts DLRs.
type Client struct {
	Session   *smpp.Session
	Accepted  atomic.Int64
	Rejected  atomic.Int64
	Delivered atomic.Int64
	Failed    atomic.Int64
	mu        sync.Mutex
	statuses  map[uint32]int
	dlrIDs    map[string]string
	mos       []string
}

// MOs returns the incoming messages received as "from|to|text".
func (c *Client) MOs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.mos...)
}

// DialClient binds as a transceiver.
func DialClient(ctx context.Context, addr, user, pass string) (*Client, error) {
	c := &Client{statuses: map[uint32]int{}, dlrIDs: map[string]string{}}
	s, err := smpp.Dial(ctx, addr, smpp.BindTransceiver, &smpp.Bind{SystemID: user, Password: pass},
		smpp.SessionOptions{Window: 50, EnquireInterval: 30 * time.Second, Handler: c.handle})
	if err != nil {
		return nil, err
	}
	c.Session = s
	return c, nil
}

func (c *Client) handle(s *smpp.Session, p *smpp.PDU) {
	if p.CommandID != smpp.DeliverSm {
		_ = s.Nack(p, smpp.StatusInvCmdID)
		return
	}
	_ = s.Respond(p, smpp.StatusOK, &smpp.MessageIDResp{})
	sm := p.Body.(*smpp.Sm)
	if sm.EsmClass&smpp.EsmDeliveryReceipt == 0 {
		c.mu.Lock()
		c.mos = append(c.mos, sm.Source+"|"+sm.Destination+"|"+string(sm.ShortMessage))
		c.mu.Unlock()
		return
	}
	if r, ok := smpp.ParseReceipt(sm); ok {
		c.mu.Lock()
		c.dlrIDs[r.ID] = r.Stat
		c.mu.Unlock()
		if r.Stat == "DELIVRD" {
			c.Delivered.Add(1)
		} else {
			c.Failed.Add(1)
		}
	}
}

// DLRFor returns the DLR status received for a message id ("" if none yet).
func (c *Client) DLRFor(id string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dlrIDs[id]
}

// StatusCount returns how many submits were answered with a command status.
func (c *Client) StatusCount(status uint32) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statuses[status]
}

// Send submits one message and returns the gateway's message id.
func (c *Client) Send(ctx context.Context, from, to, text string) (string, error) {
	resp, err := c.Session.Request(ctx, smpp.SubmitSm, &smpp.Sm{
		SourceTon: 5, Source: from, DestTon: 1, DestNpi: 1, Destination: to, RegisteredDelivery: 1, ShortMessage: []byte(text),
	})
	if err != nil {
		c.Rejected.Add(1)
		if se, ok := err.(*smpp.StatusError); ok {
			c.mu.Lock()
			c.statuses[se.Status]++
			c.mu.Unlock()
		}
		return "", err
	}
	c.Accepted.Add(1)
	return resp.Body.(*smpp.MessageIDResp).MessageID, nil
}

// Blast sends n messages at tps to random numbers with the given prefix and returns the elapsed time.
func (c *Client) Blast(ctx context.Context, n, tps int, from, prefix string) time.Duration {
	start := time.Now()
	tick := time.NewTicker(time.Second / time.Duration(max(tps, 1)))
	defer tick.Stop()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		select {
		case <-ctx.Done():
			wg.Wait()
			return time.Since(start)
		case <-tick.C:
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = c.Send(ctx, from, fmt.Sprintf("%s%07d", prefix, i), fmt.Sprintf("load test message %d", i))
		}(i)
	}
	wg.Wait()
	return time.Since(start)
}
