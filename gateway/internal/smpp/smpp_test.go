package smpp

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"
)

func TestSubmitSmRoundTrip(t *testing.T) {
	in := &PDU{CommandID: SubmitSm, Sequence: 7, Body: &Sm{
		SourceTon: 5, Source: "Telivoz", DestTon: 1, DestNpi: 1, Destination: "923001234567",
		RegisteredDelivery: 1, DataCoding: 8, ShortMessage: []byte{0x00, 0x48, 0x00, 0x69},
		TLVs: map[uint16][]byte{TagSarMsgRefNum: {0x00, 0x01}},
	}}
	data, err := in.Encode()
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	sm := out.Body.(*Sm)
	if out.Sequence != 7 || sm.Source != "Telivoz" || sm.Destination != "923001234567" || sm.DataCoding != 8 {
		t.Fatalf("unexpected decode: %+v %+v", out, sm)
	}
	if !bytes.Equal(sm.ShortMessage, []byte{0x00, 0x48, 0x00, 0x69}) || !bytes.Equal(sm.TLVs[TagSarMsgRefNum], []byte{0, 1}) {
		t.Fatalf("payload or tlv mismatch: %+v", sm)
	}
}

func TestDecodeRejectsBadLength(t *testing.T) {
	data, _ := (&PDU{CommandID: EnquireLink, Sequence: 1}).Encode()
	if _, err := Decode(data[:15]); err == nil {
		t.Fatal("expected error for short pdu")
	}
	data[3] = 99
	if _, err := Decode(data); err == nil {
		t.Fatal("expected error for wrong length field")
	}
}

func TestParseReceiptText(t *testing.T) {
	sm := &Sm{EsmClass: EsmDeliveryReceipt, ShortMessage: []byte(
		"id:0A1B2C sub:001 dlvrd:001 submit date:2610091200 done date:2610091201 stat:DELIVRD err:000 text:Hello world")}
	r, ok := ParseReceipt(sm)
	if !ok || r.ID != "0A1B2C" || r.Stat != "DELIVRD" || r.Err != "000" || r.Text != "Hello world" || r.DoneAt.Minute() != 1 {
		t.Fatalf("unexpected receipt: %+v", r)
	}
}

func TestParseReceiptPrefersTLVs(t *testing.T) {
	sm := &Sm{EsmClass: EsmDeliveryReceipt, ShortMessage: []byte("id:123 stat:DELIVRD"),
		TLVs: map[uint16][]byte{TagReceiptedMessageID: []byte("7B\x00"), TagMessageState: {5}}}
	r, ok := ParseReceipt(sm)
	if !ok || r.ID != "7B" || r.Stat != "UNDELIV" {
		t.Fatalf("unexpected receipt: %+v", r)
	}
}

func TestReceiptRoundTrip(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	r := &Receipt{ID: "abc", SubmitAt: now, DoneAt: now, Stat: "UNDELIV", Err: "001"}
	got, ok := ParseReceipt(r.Sm("923001234567", "Telivoz", 1, 1, 5, 0))
	if !ok || got.ID != "abc" || got.Stat != "UNDELIV" || got.Err != "001" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestAlternateIDs(t *testing.T) {
	ids := AlternateIDs("255")
	if ids[0] != "ff" {
		t.Fatalf("want ff first, got %v", ids)
	}
}

func TestSessionRequestResponseAndBind(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		var srv *Session
		srv = NewSession(conn, SessionOptions{Handler: func(s *Session, p *PDU) {
			switch p.CommandID {
			case BindTransceiver:
				b := p.Body.(*Bind)
				if b.Password != "secret" {
					_ = s.Respond(p, StatusInvPaswd, &BindResp{})
					return
				}
				_ = s.Respond(p, StatusOK, &BindResp{SystemID: "gw"})
			case SubmitSm:
				_ = s.Respond(p, StatusOK, &MessageIDResp{MessageID: "m-1"})
			}
		}})
		srv.Serve()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := Dial(ctx, ln.Addr().String(), BindTransceiver, &Bind{SystemID: "u", Password: "secret"}, SessionOptions{EnquireInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	resp, err := s.Request(ctx, SubmitSm, &Sm{Destination: "1", ShortMessage: []byte("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if id := resp.Body.(*MessageIDResp).MessageID; id != "m-1" {
		t.Fatalf("want m-1, got %q", id)
	}
	// keep-alive: stay idle past one enquire interval, the session must remain usable
	time.Sleep(2500 * time.Millisecond)
	if _, err := s.Request(ctx, SubmitSm, &Sm{Destination: "1"}); err != nil {
		t.Fatalf("session died during idle: %v", err)
	}
}

func TestBindWrongPassword(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		conn, _ := ln.Accept()
		NewSession(conn, SessionOptions{Handler: func(s *Session, p *PDU) {
			_ = s.Respond(p, StatusInvPaswd, &BindResp{})
		}}).Serve()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := Dial(ctx, ln.Addr().String(), BindTransmitter, &Bind{SystemID: "u", Password: "x"}, SessionOptions{}); err == nil {
		t.Fatal("expected bind failure")
	}
}
