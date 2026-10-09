package smpp

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrClosed  = errors.New("smpp: session closed")
	ErrTimeout = errors.New("smpp: response timeout")
	// ErrAwaitingClosed means the request was written but the connection closed before the answer arrived:
	// the peer may have accepted it, so it must not be blindly resent.
	ErrAwaitingClosed = errors.New("smpp: session closed while awaiting response")
)

// StatusError is returned by Request when the peer answers with a non-zero command status.
type StatusError struct{ Status uint32 }

func (e *StatusError) Error() string { return fmt.Sprintf("smpp: command status 0x%08x", e.Status) }

// Handler processes an incoming request PDU. It must answer with s.Respond (or s.Nack).
type Handler func(s *Session, p *PDU)

type SessionOptions struct {
	Window          int           // max outstanding requests sent by us
	EnquireInterval time.Duration // keep-alive interval (0 disables)
	ResponseTimeout time.Duration
	MaxInflight     int // max requests from the peer processed in parallel
	Handler         Handler
	OnClose         func(*Session)
}

// Session is one SMPP connection, either side.
type Session struct {
	conn    net.Conn
	opts    SessionOptions
	wmu     sync.Mutex
	seq     atomic.Uint32
	pmu     sync.Mutex
	pending map[uint32]chan *PDU
	window  chan struct{}
	inflight chan struct{}
	closed  chan struct{}
	once    sync.Once
	lastRx  atomic.Int64

	// Set by the owner after a successful bind.
	BindCommand uint32
	SystemID    string
	Data        any
}

func NewSession(conn net.Conn, opts SessionOptions) *Session {
	if opts.Window <= 0 {
		opts.Window = 10
	}
	if opts.ResponseTimeout <= 0 {
		opts.ResponseTimeout = 30 * time.Second
	}
	if opts.MaxInflight <= 0 {
		opts.MaxInflight = 64
	}
	s := &Session{
		conn:     conn,
		opts:     opts,
		pending:  map[uint32]chan *PDU{},
		window:   make(chan struct{}, opts.Window),
		inflight: make(chan struct{}, opts.MaxInflight),
		closed:   make(chan struct{}),
	}
	s.lastRx.Store(time.Now().UnixNano())
	return s
}

func (s *Session) RemoteAddr() net.Addr { return s.conn.RemoteAddr() }
func (s *Session) Done() <-chan struct{} { return s.closed }

// CanReceive reports whether the peer bound in a mode that accepts deliver_sm.
func (s *Session) CanReceive() bool {
	return s.BindCommand == BindReceiver || s.BindCommand == BindTransceiver
}

// CanTransmit reports whether the peer bound in a mode that may send submit_sm.
func (s *Session) CanTransmit() bool {
	return s.BindCommand == BindTransmitter || s.BindCommand == BindTransceiver
}

// Serve reads PDUs until the connection closes. It blocks.
func (s *Session) Serve() {
	defer s.Close()
	if s.opts.EnquireInterval > 0 {
		go s.keepAlive()
	}
	br := bufio.NewReaderSize(s.conn, 64*1024)
	hdr := make([]byte, 4)
	for {
		if s.opts.EnquireInterval > 0 {
			_ = s.conn.SetReadDeadline(time.Now().Add(3*s.opts.EnquireInterval + 10*time.Second))
		}
		if _, err := io.ReadFull(br, hdr); err != nil {
			return
		}
		n := binary.BigEndian.Uint32(hdr)
		if n < headerLen || n > MaxPDULen {
			return
		}
		buf := make([]byte, n)
		copy(buf, hdr)
		if _, err := io.ReadFull(br, buf[4:]); err != nil {
			return
		}
		s.lastRx.Store(time.Now().UnixNano())
		p, err := Decode(buf)
		if err != nil {
			seq := binary.BigEndian.Uint32(buf[12:16])
			_ = s.Send(&PDU{CommandID: GenericNack, Status: StatusInvCmdLen, Sequence: seq})
			continue
		}
		if p.IsResponse() {
			s.deliverResponse(p)
			continue
		}
		switch p.CommandID {
		case EnquireLink:
			_ = s.Send(&PDU{CommandID: EnquireLinkResp, Sequence: p.Sequence})
		case Unbind:
			_ = s.Send(&PDU{CommandID: UnbindResp, Sequence: p.Sequence})
			return
		default:
			if s.opts.Handler == nil {
				_ = s.Nack(p, StatusInvCmdID)
				continue
			}
			select {
			case s.inflight <- struct{}{}:
			case <-s.closed:
				return
			}
			go func() {
				defer func() { <-s.inflight }()
				s.opts.Handler(s, p)
			}()
		}
	}
}

func (s *Session) deliverResponse(p *PDU) {
	s.pmu.Lock()
	ch, ok := s.pending[p.Sequence]
	if ok {
		delete(s.pending, p.Sequence)
	}
	s.pmu.Unlock()
	if ok {
		ch <- p
	}
}

func (s *Session) keepAlive() {
	t := time.NewTicker(s.opts.EnquireInterval)
	defer t.Stop()
	for {
		select {
		case <-s.closed:
			return
		case <-t.C:
			// Only probe when the line has been quiet.
			if time.Since(time.Unix(0, s.lastRx.Load())) < s.opts.EnquireInterval {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), s.opts.ResponseTimeout)
			_, err := s.Request(ctx, EnquireLink, nil)
			cancel()
			if err != nil {
				s.Close()
				return
			}
		}
	}
}

// NextSequence returns a new sequence number (1..0x7FFFFFFF).
func (s *Session) NextSequence() uint32 {
	for {
		v := s.seq.Add(1) & 0x7FFFFFFF
		if v != 0 {
			return v
		}
	}
}

// Send writes one PDU.
func (s *Session) Send(p *PDU) error {
	data, err := p.Encode()
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	select {
	case <-s.closed:
		return ErrClosed
	default:
	}
	_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := s.conn.Write(data); err != nil {
		s.Close()
		return err
	}
	return nil
}

// Request sends a request and waits for its response, respecting the send window.
// A non-OK command status is returned as *StatusError together with the response.
func (s *Session) Request(ctx context.Context, cmd uint32, body any) (*PDU, error) {
	select {
	case s.window <- struct{}{}:
	case <-s.closed:
		return nil, ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.window }()

	seq := s.NextSequence()
	ch := make(chan *PDU, 1)
	s.pmu.Lock()
	s.pending[seq] = ch
	s.pmu.Unlock()
	cleanup := func() {
		s.pmu.Lock()
		delete(s.pending, seq)
		s.pmu.Unlock()
	}

	if err := s.Send(&PDU{CommandID: cmd, Sequence: seq, Body: body}); err != nil {
		cleanup()
		return nil, err
	}
	timer := time.NewTimer(s.opts.ResponseTimeout)
	defer timer.Stop()
	select {
	case resp := <-ch:
		if resp.CommandID == GenericNack {
			return resp, &StatusError{Status: resp.Status}
		}
		if resp.Status != StatusOK {
			return resp, &StatusError{Status: resp.Status}
		}
		return resp, nil
	case <-timer.C:
		cleanup()
		return nil, ErrTimeout
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	case <-s.closed:
		cleanup()
		return nil, ErrAwaitingClosed
	}
}

// Respond answers a request with the matching response command.
func (s *Session) Respond(req *PDU, status uint32, body any) error {
	return s.Send(&PDU{CommandID: req.CommandID | 0x80000000, Status: status, Sequence: req.Sequence, Body: body})
}

// Nack answers a request with generic_nack.
func (s *Session) Nack(req *PDU, status uint32) error {
	return s.Send(&PDU{CommandID: GenericNack, Status: status, Sequence: req.Sequence})
}

// Unbind politely ends the session.
func (s *Session) Unbind(ctx context.Context) {
	_, _ = s.Request(ctx, Unbind, nil)
	s.Close()
}

// Close closes the connection once and fails all waiting requests.
func (s *Session) Close() {
	s.once.Do(func() {
		close(s.closed)
		_ = s.conn.Close()
		if s.opts.OnClose != nil {
			go s.opts.OnClose(s)
		}
	})
}

// Dial connects to an SMPP server and binds. The returned session is already serving.
func Dial(ctx context.Context, addr string, bindCmd uint32, bind *Bind, opts SessionOptions) (*Session, error) {
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	s := NewSession(conn, opts)
	go s.Serve()
	if bind.InterfaceVersion == 0 {
		bind.InterfaceVersion = 0x34
	}
	if _, err := s.Request(ctx, bindCmd, bind); err != nil {
		s.Close()
		return nil, fmt.Errorf("bind: %w", err)
	}
	s.BindCommand = bindCmd
	s.SystemID = bind.SystemID
	return s, nil
}
