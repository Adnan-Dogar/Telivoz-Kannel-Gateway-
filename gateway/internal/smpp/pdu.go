// Package smpp implements the SMPP 3.4 protocol: PDU encoding, sessions, and delivery receipts.
// It is used both as a server (clients bind to the gateway) and as a client (the gateway binds to vendors).
package smpp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// Command IDs.
const (
	GenericNack         uint32 = 0x80000000
	BindReceiver        uint32 = 0x00000001
	BindReceiverResp    uint32 = 0x80000001
	BindTransmitter     uint32 = 0x00000002
	BindTransmitterResp uint32 = 0x80000002
	QuerySm             uint32 = 0x00000003
	QuerySmResp         uint32 = 0x80000003
	SubmitSm            uint32 = 0x00000004
	SubmitSmResp        uint32 = 0x80000004
	DeliverSm           uint32 = 0x00000005
	DeliverSmResp       uint32 = 0x80000005
	Unbind              uint32 = 0x00000006
	UnbindResp          uint32 = 0x80000006
	BindTransceiver     uint32 = 0x00000009
	BindTransceiverResp uint32 = 0x80000009
	EnquireLink         uint32 = 0x00000015
	EnquireLinkResp     uint32 = 0x80000015
)

// Command status codes.
const (
	StatusOK            uint32 = 0x00000000
	StatusInvMsgLen     uint32 = 0x00000001
	StatusInvCmdLen     uint32 = 0x00000002
	StatusInvCmdID      uint32 = 0x00000003
	StatusInvBnd        uint32 = 0x00000004
	StatusAlreadyBound  uint32 = 0x00000005
	StatusSysErr        uint32 = 0x00000008
	StatusInvSrcAdr     uint32 = 0x0000000A
	StatusInvDstAdr     uint32 = 0x0000000B
	StatusBindFail      uint32 = 0x0000000D
	StatusInvPaswd      uint32 = 0x0000000E
	StatusInvSysID      uint32 = 0x0000000F
	StatusMsgQFull      uint32 = 0x00000014
	StatusSubmitFail    uint32 = 0x00000045
	StatusThrottled     uint32 = 0x00000058
	StatusTempAppErr    uint32 = 0x00000064
	StatusPermAppErr    uint32 = 0x00000065
	StatusRejectAppErr  uint32 = 0x00000066
	StatusInvDataCoding uint32 = 0x00000104
)

// Optional parameter (TLV) tags used by the gateway.
const (
	TagReceiptedMessageID uint16 = 0x001E
	TagSarMsgRefNum       uint16 = 0x020C
	TagSarTotalSegments   uint16 = 0x020E
	TagSarSegmentSeqnum   uint16 = 0x020F
	TagMessagePayload     uint16 = 0x0424
	TagMessageState       uint16 = 0x0427
)

// ESM class bits.
const (
	EsmUDHI            byte = 0x40
	EsmDeliveryReceipt byte = 0x04
)

const (
	headerLen = 16
	// MaxPDULen guards against garbage lengths from broken peers.
	MaxPDULen = 64 * 1024
)

var ErrPDUTooLarge = errors.New("smpp: pdu too large")

// PDU is one SMPP packet. Body holds the decoded fields for the commands the gateway handles.
type PDU struct {
	CommandID uint32
	Status    uint32
	Sequence  uint32
	Body      any
}

// IsResponse reports whether the command is a response (high bit set).
func (p *PDU) IsResponse() bool { return p.CommandID&0x80000000 != 0 }

// Bind is the body of bind_transmitter, bind_receiver and bind_transceiver.
type Bind struct {
	SystemID         string
	Password         string
	SystemType       string
	InterfaceVersion byte
	AddrTon          byte
	AddrNpi          byte
	AddressRange     string
}

// BindResp is the body of the bind responses.
type BindResp struct {
	SystemID string
}

// Sm is the body of submit_sm and deliver_sm (same layout).
type Sm struct {
	ServiceType          string
	SourceTon            byte
	SourceNpi            byte
	Source               string
	DestTon              byte
	DestNpi              byte
	Destination          string
	EsmClass             byte
	ProtocolID           byte
	PriorityFlag         byte
	ScheduleDeliveryTime string
	ValidityPeriod       string
	RegisteredDelivery   byte
	ReplaceIfPresent     byte
	DataCoding           byte
	SmDefaultMsgID       byte
	ShortMessage         []byte
	TLVs                 map[uint16][]byte
}

// Payload returns the message bytes, preferring the message_payload TLV when short_message is empty.
func (s *Sm) Payload() []byte {
	if len(s.ShortMessage) == 0 {
		if v, ok := s.TLVs[TagMessagePayload]; ok {
			return v
		}
	}
	return s.ShortMessage
}

// MessageIDResp is the body of submit_sm_resp and deliver_sm_resp.
type MessageIDResp struct {
	MessageID string
}

// Encode serialises the PDU, including the header.
func (p *PDU) Encode() ([]byte, error) {
	var b bytes.Buffer
	b.Write(make([]byte, headerLen))
	switch body := p.Body.(type) {
	case nil:
	case *Bind:
		writeCString(&b, body.SystemID)
		writeCString(&b, body.Password)
		writeCString(&b, body.SystemType)
		b.WriteByte(body.InterfaceVersion)
		b.WriteByte(body.AddrTon)
		b.WriteByte(body.AddrNpi)
		writeCString(&b, body.AddressRange)
	case *BindResp:
		writeCString(&b, body.SystemID)
	case *Sm:
		if len(body.ShortMessage) > 254 {
			return nil, fmt.Errorf("smpp: short_message longer than 254 bytes (%d)", len(body.ShortMessage))
		}
		writeCString(&b, body.ServiceType)
		b.WriteByte(body.SourceTon)
		b.WriteByte(body.SourceNpi)
		writeCString(&b, body.Source)
		b.WriteByte(body.DestTon)
		b.WriteByte(body.DestNpi)
		writeCString(&b, body.Destination)
		b.WriteByte(body.EsmClass)
		b.WriteByte(body.ProtocolID)
		b.WriteByte(body.PriorityFlag)
		writeCString(&b, body.ScheduleDeliveryTime)
		writeCString(&b, body.ValidityPeriod)
		b.WriteByte(body.RegisteredDelivery)
		b.WriteByte(body.ReplaceIfPresent)
		b.WriteByte(body.DataCoding)
		b.WriteByte(body.SmDefaultMsgID)
		b.WriteByte(byte(len(body.ShortMessage)))
		b.Write(body.ShortMessage)
		writeTLVs(&b, body.TLVs)
	case *MessageIDResp:
		writeCString(&b, body.MessageID)
	default:
		return nil, fmt.Errorf("smpp: cannot encode body %T", p.Body)
	}
	out := b.Bytes()
	binary.BigEndian.PutUint32(out[0:4], uint32(len(out)))
	binary.BigEndian.PutUint32(out[4:8], p.CommandID)
	binary.BigEndian.PutUint32(out[8:12], p.Status)
	binary.BigEndian.PutUint32(out[12:16], p.Sequence)
	return out, nil
}

// Decode parses one complete PDU (header included). Unknown commands decode with a nil body.
func Decode(data []byte) (*PDU, error) {
	if len(data) < headerLen {
		return nil, fmt.Errorf("smpp: pdu shorter than header")
	}
	length := binary.BigEndian.Uint32(data[0:4])
	if int(length) != len(data) {
		return nil, fmt.Errorf("smpp: length field %d does not match %d bytes", length, len(data))
	}
	p := &PDU{
		CommandID: binary.BigEndian.Uint32(data[4:8]),
		Status:    binary.BigEndian.Uint32(data[8:12]),
		Sequence:  binary.BigEndian.Uint32(data[12:16]),
	}
	r := &reader{buf: data[headerLen:]}
	switch p.CommandID {
	case BindReceiver, BindTransmitter, BindTransceiver:
		p.Body = &Bind{
			SystemID: r.cstring(), Password: r.cstring(), SystemType: r.cstring(),
			InterfaceVersion: r.byte(), AddrTon: r.byte(), AddrNpi: r.byte(), AddressRange: r.cstring(),
		}
	case BindReceiverResp, BindTransmitterResp, BindTransceiverResp:
		// Error responses may carry no body at all.
		if len(r.buf) > 0 {
			p.Body = &BindResp{SystemID: r.cstring()}
		} else {
			p.Body = &BindResp{}
		}
	case SubmitSm, DeliverSm:
		sm := &Sm{}
		sm.ServiceType = r.cstring()
		sm.SourceTon, sm.SourceNpi, sm.Source = r.byte(), r.byte(), r.cstring()
		sm.DestTon, sm.DestNpi, sm.Destination = r.byte(), r.byte(), r.cstring()
		sm.EsmClass, sm.ProtocolID, sm.PriorityFlag = r.byte(), r.byte(), r.byte()
		sm.ScheduleDeliveryTime, sm.ValidityPeriod = r.cstring(), r.cstring()
		sm.RegisteredDelivery, sm.ReplaceIfPresent = r.byte(), r.byte()
		sm.DataCoding, sm.SmDefaultMsgID = r.byte(), r.byte()
		sm.ShortMessage = r.bytes(int(r.byte()))
		sm.TLVs = r.tlvs()
		p.Body = sm
	case SubmitSmResp, DeliverSmResp:
		if len(r.buf) > 0 {
			p.Body = &MessageIDResp{MessageID: r.cstring()}
		} else {
			p.Body = &MessageIDResp{}
		}
	}
	if r.err != nil {
		return nil, fmt.Errorf("smpp: decode command 0x%08x: %w", p.CommandID, r.err)
	}
	return p, nil
}

func writeCString(b *bytes.Buffer, s string) {
	b.WriteString(s)
	b.WriteByte(0)
}

func writeTLVs(b *bytes.Buffer, tlvs map[uint16][]byte) {
	// Deterministic order keeps encoded PDUs stable for tests.
	tags := make([]uint16, 0, len(tlvs))
	for t := range tlvs {
		tags = append(tags, t)
	}
	for i := 1; i < len(tags); i++ {
		for j := i; j > 0 && tags[j] < tags[j-1]; j-- {
			tags[j], tags[j-1] = tags[j-1], tags[j]
		}
	}
	for _, t := range tags {
		v := tlvs[t]
		var hdr [4]byte
		binary.BigEndian.PutUint16(hdr[0:2], t)
		binary.BigEndian.PutUint16(hdr[2:4], uint16(len(v)))
		b.Write(hdr[:])
		b.Write(v)
	}
}

type reader struct {
	buf []byte
	err error
}

func (r *reader) fail(msg string) {
	if r.err == nil {
		r.err = errors.New(msg)
	}
}

func (r *reader) byte() byte {
	if len(r.buf) < 1 {
		r.fail("unexpected end of body")
		return 0
	}
	v := r.buf[0]
	r.buf = r.buf[1:]
	return v
}

func (r *reader) bytes(n int) []byte {
	if len(r.buf) < n {
		r.fail("unexpected end of body")
		return nil
	}
	v := append([]byte(nil), r.buf[:n]...)
	r.buf = r.buf[n:]
	return v
}

func (r *reader) cstring() string {
	i := bytes.IndexByte(r.buf, 0)
	if i < 0 {
		r.fail("unterminated c-octet string")
		return ""
	}
	s := string(r.buf[:i])
	r.buf = r.buf[i+1:]
	return s
}

func (r *reader) tlvs() map[uint16][]byte {
	if len(r.buf) == 0 {
		return nil
	}
	m := map[uint16][]byte{}
	for len(r.buf) >= 4 && r.err == nil {
		tag := binary.BigEndian.Uint16(r.buf[0:2])
		n := int(binary.BigEndian.Uint16(r.buf[2:4]))
		r.buf = r.buf[4:]
		m[tag] = r.bytes(n)
	}
	return m
}
