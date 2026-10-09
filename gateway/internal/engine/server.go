package engine

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/auth"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/gsm"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/smpp"
)

// ServeSMPP accepts client SMPP connections until ctx is cancelled.
func (e *Engine) ServeSMPP(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return e.ServeSMPPListener(ctx, ln)
}

// ServeSMPPListener serves client SMPP connections on an existing listener.
func (e *Engine) ServeSMPPListener(ctx context.Context, ln net.Listener) error {
	e.log.Info("SMPP server listening", "addr", ln.Addr().String())
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		sess := smpp.NewSession(conn, smpp.SessionOptions{
			Window: 20, EnquireInterval: 30 * time.Second, ResponseTimeout: 30 * time.Second, MaxInflight: 128,
			Handler: e.handleClientPDU,
			OnClose: func(s *smpp.Session) {
				if a, ok := s.Data.(*Account); ok {
					e.clients.remove(a.ID, s)
					e.log.Info("client unbound", "account", a.Username, "remote", s.RemoteAddr().String())
				}
			},
		})
		go sess.Serve()
		// A connection must bind within 30 seconds.
		go func() {
			select {
			case <-sess.Done():
			case <-time.After(30 * time.Second):
				if sess.Data == nil {
					sess.Close()
				}
			}
		}()
	}
}

func (e *Engine) handleClientPDU(s *smpp.Session, p *smpp.PDU) {
	switch p.CommandID {
	case smpp.BindTransceiver, smpp.BindTransmitter, smpp.BindReceiver:
		e.handleBind(s, p)
	case smpp.SubmitSm:
		e.handleSubmit(s, p)
	default:
		_ = s.Nack(p, smpp.StatusInvCmdID)
	}
}

func ipAllowed(remote net.Addr, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(remote.String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if strings.Contains(a, "/") {
			if _, n, err := net.ParseCIDR(a); err == nil && n.Contains(ip) {
				return true
			}
		} else if other := net.ParseIP(a); other != nil && other.Equal(ip) {
			return true
		}
	}
	return false
}

func (e *Engine) handleBind(s *smpp.Session, p *smpp.PDU) {
	if s.Data != nil {
		_ = s.Respond(p, smpp.StatusAlreadyBound, &smpp.BindResp{})
		return
	}
	b := p.Body.(*smpp.Bind)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	acc, hash, ips, err := e.LoadAccount(ctx, "smpp", b.SystemID)
	fail := func(status uint32, reason string) {
		e.log.Warn("client bind rejected", "system_id", b.SystemID, "remote", s.RemoteAddr().String(), "reason", reason)
		_ = s.Respond(p, status, &smpp.BindResp{})
		time.AfterFunc(time.Second, s.Close)
	}
	switch {
	case err != nil:
		fail(smpp.StatusInvSysID, "unknown or disabled account")
	case !auth.CheckPassword(hash, b.Password):
		fail(smpp.StatusInvPaswd, "wrong password")
	case !ipAllowed(s.RemoteAddr(), ips):
		fail(smpp.StatusBindFail, "ip not allowed")
	case e.clients.count(acc.ID) >= acc.MaxBinds:
		fail(smpp.StatusBindFail, "too many binds")
	default:
		s.BindCommand = p.CommandID
		s.SystemID = b.SystemID
		s.Data = &acc
		e.clients.add(acc.ID, s)
		_ = s.Respond(p, smpp.StatusOK, &smpp.BindResp{SystemID: "Telivoz"})
		e.log.Info("client bound", "account", acc.Username, "remote", s.RemoteAddr().String())
		if s.CanReceive() {
			go e.retryDLRs(context.Background(), acc.ID)
		}
	}
}

func (e *Engine) handleSubmit(s *smpp.Session, p *smpp.PDU) {
	acc, ok := s.Data.(*Account)
	if !ok || !s.CanTransmit() {
		_ = s.Respond(p, smpp.StatusInvBnd, &smpp.MessageIDResp{})
		return
	}
	sm := p.Body.(*smpp.Sm)
	payload := sm.Payload()
	text, udh := gsm.Decode(sm.DataCoding, payload, sm.EsmClass&smpp.EsmUDHI != 0)
	body := payload[len(udh):]
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := e.Submit(ctx, SubmitRequest{
		Account: *acc, Source: sm.Source, Destination: sm.Destination, Text: text,
		DataCoding: sm.DataCoding, Payload: body, UDH: udh, WantsDLR: sm.RegisteredDelivery&0x03 != 0,
	})
	if err != nil {
		status := smpp.StatusSysErr
		var se *SubmitError
		if errors.As(err, &se) {
			status = se.SMPPStatus
		}
		_ = s.Respond(p, status, &smpp.MessageIDResp{})
		return
	}
	_ = s.Respond(p, smpp.StatusOK, &smpp.MessageIDResp{MessageID: res.MessageID.String()})
}
