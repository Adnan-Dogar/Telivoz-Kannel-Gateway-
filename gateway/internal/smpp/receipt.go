package smpp

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

// Receipt is a delivery receipt (DLR) carried in deliver_sm.
type Receipt struct {
	ID        string
	Submitted int
	Delivered int
	SubmitAt  time.Time
	DoneAt    time.Time
	Stat      string // DELIVRD, UNDELIV, EXPIRED, REJECTD, DELETED, ACCEPTD, UNKNOWN, ENROUTE
	Err       string
	Text      string
}

// Message states for the message_state TLV.
var messageStates = map[string]byte{
	"ENROUTE": 1, "DELIVRD": 2, "EXPIRED": 3, "DELETED": 4, "UNDELIV": 5, "ACCEPTD": 6, "UNKNOWN": 7, "REJECTD": 8,
}

var stateNames = map[byte]string{1: "ENROUTE", 2: "DELIVRD", 3: "EXPIRED", 4: "DELETED", 5: "UNDELIV", 6: "ACCEPTD", 7: "UNKNOWN", 8: "REJECTD"}

var receiptField = regexp.MustCompile(`(?i)(id|sub|dlvrd|submit date|done date|stat|err|text):\s*`)

// ParseReceipt reads a receipt from a deliver_sm. TLVs (receipted_message_id, message_state) take precedence
// over the text, because many vendors put a truncated or differently formatted id in the text.
func ParseReceipt(sm *Sm) (*Receipt, bool) {
	if sm.EsmClass&EsmDeliveryReceipt == 0 && sm.TLVs[TagReceiptedMessageID] == nil {
		return nil, false
	}
	r := &Receipt{}
	text := string(sm.ShortMessage)
	locs := receiptField.FindAllStringSubmatchIndex(text, -1)
	for i, loc := range locs {
		key := strings.ToLower(text[loc[2]:loc[3]])
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		val := strings.TrimSpace(text[loc[1]:end])
		switch key {
		case "id":
			r.ID = val
		case "sub":
			fmt.Sscanf(val, "%d", &r.Submitted)
		case "dlvrd":
			fmt.Sscanf(val, "%d", &r.Delivered)
		case "submit date":
			r.SubmitAt = parseReceiptTime(val)
		case "done date":
			r.DoneAt = parseReceiptTime(val)
		case "stat":
			r.Stat = strings.ToUpper(val)
		case "err":
			r.Err = val
		case "text":
			r.Text = val
		}
	}
	if v := sm.TLVs[TagReceiptedMessageID]; len(v) > 0 {
		r.ID = strings.TrimRight(string(v), "\x00")
	}
	if v := sm.TLVs[TagMessageState]; len(v) == 1 {
		if name, ok := stateNames[v[0]]; ok {
			r.Stat = name
		}
	}
	if r.ID == "" {
		return nil, false
	}
	if r.Stat == "" {
		r.Stat = "UNKNOWN"
	}
	return r, true
}

func parseReceiptTime(v string) time.Time {
	for _, layout := range []string{"0601021504", "060102150405"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

// Sm builds the deliver_sm body for this receipt, addressed back to the original sender.
func (r *Receipt) Sm(source, destination string, sourceTon, sourceNpi, destTon, destNpi byte) *Sm {
	delivered := 0
	if r.Stat == "DELIVRD" {
		delivered = 1
	}
	text := r.Text
	if len(text) > 20 {
		text = text[:20]
	}
	errCode := r.Err
	if errCode == "" {
		errCode = "000"
	}
	body := fmt.Sprintf("id:%s sub:001 dlvrd:%03d submit date:%s done date:%s stat:%s err:%s text:%s",
		r.ID, delivered, r.SubmitAt.UTC().Format("0601021504"), r.DoneAt.UTC().Format("0601021504"), r.Stat, errCode, text)
	tlvs := map[uint16][]byte{TagReceiptedMessageID: append([]byte(r.ID), 0)}
	if st, ok := messageStates[r.Stat]; ok {
		tlvs[TagMessageState] = []byte{st}
	}
	return &Sm{
		SourceTon: sourceTon, SourceNpi: sourceNpi, Source: source,
		DestTon: destTon, DestNpi: destNpi, Destination: destination,
		EsmClass: EsmDeliveryReceipt, ShortMessage: []byte(body), TLVs: tlvs,
	}
}

// AlternateIDs returns the other common spellings of a vendor message ID (hex <-> decimal), used when a
// vendor reports DLR ids in a different base than its submit_sm_resp.
func AlternateIDs(id string) []string {
	var out []string
	if n, ok := new(big.Int).SetString(id, 10); ok {
		out = append(out, strings.ToLower(n.Text(16)), strings.ToUpper(n.Text(16)))
	}
	if n, ok := new(big.Int).SetString(id, 16); ok {
		out = append(out, n.Text(10))
	}
	if t := strings.TrimLeft(id, "0"); t != id && t != "" {
		out = append(out, t)
	}
	return out
}
