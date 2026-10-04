package main

import (
	"database/sql"
	"fmt"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"strings"
	"time"
)

func ensureCallsTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS calls (
 call_id TEXT PRIMARY KEY, caller_jid TEXT NOT NULL, call_type TEXT DEFAULT 'voice',
 timestamp TIMESTAMP NOT NULL, summary TEXT, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`)
	return err
}

type callObservation struct {
	id     string
	remote types.JID
	media  string
	at     time.Time
}

func callMedia(data *waBinary.Node) string {
	if data != nil {
		if _, ok := data.GetOptionalChildByTag("video"); ok {
			return "video"
		}
		if _, ok := data.GetOptionalChildByTag("audio"); ok {
			return "voice"
		}
	}
	return "unknown"
}

func callPartner(meta types.BasicCallMeta, ownPN *types.JID, ownLID types.JID) (types.JID, bool) {
	if ownPN == nil || ownPN.IsEmpty() || !meta.GroupJID.IsEmpty() {
		return types.EmptyJID, false
	}
	isOwn := func(j types.JID) bool {
		return !j.IsEmpty() && (j.ToNonAD() == ownPN.ToNonAD() || (!ownLID.IsEmpty() && j.ToNonAD() == ownLID.ToNonAD()))
	}
	valid := func(j types.JID) bool {
		return !j.IsEmpty() && (j.Server == types.DefaultUserServer || j.Server == types.HiddenUserServer)
	}
	creator := meta.CallCreator.ToNonAD()
	var partner types.JID
	if isOwn(creator) {
		partner = meta.From.ToNonAD()
	} else {
		partner = creator
		if creator.Server == types.HiddenUserServer && meta.CallCreatorAlt.Server == types.DefaultUserServer {
			partner = meta.CallCreatorAlt.ToNonAD()
		}
	}
	if !valid(partner) || isOwn(partner) {
		return types.EmptyJID, false
	}
	return partner, true
}

func recordCallEvent(store *MessageStore, evt any, ownPN *types.JID, ownLID types.JID) (bool, error) {
	var meta types.BasicCallMeta
	media := "unknown"
	switch v := evt.(type) {
	case *events.CallOffer:
		meta = v.BasicCallMeta
		media = callMedia(v.Data)
	case *events.CallAccept:
		meta = v.BasicCallMeta
	case *events.CallTerminate:
		meta = v.BasicCallMeta
	case *events.CallReject:
		meta = v.BasicCallMeta
	case *events.CallOfferNotice:
		if v.Type == "group" {
			return true, nil
		}
		meta = v.BasicCallMeta
		if v.Media == "video" {
			media = "video"
		} else if v.Media == "audio" {
			media = "voice"
		}
	default:
		return false, nil
	}
	if strings.TrimSpace(meta.CallID) == "" || meta.Timestamp.IsZero() {
		return true, nil
	}
	partner, ok := callPartner(meta, ownPN, ownLID)
	if !ok {
		return true, nil
	}
	return true, store.storeCallObservation(callObservation{meta.CallID, partner, media, meta.Timestamp})
}

func (store *MessageStore) storeCallObservation(o callObservation) error {
	// CallID is the existing primary key. Preserve owner summaries and knownmedia;
	// a later accept/terminate never turns observed video into unknown/voice.
	result, err := store.db.Exec(`INSERT INTO calls(call_id,caller_jid,call_type,timestamp)
 VALUES(?,?,?,?) ON CONFLICT(call_id) DO UPDATE SET
 call_type=CASE WHEN calls.call_type='unknown' THEN excluded.call_type ELSE calls.call_type END,
 timestamp=CASE WHEN julianday(excluded.timestamp)<julianday(calls.timestamp) THEN excluded.timestamp ELSE calls.timestamp END
 WHERE calls.caller_jid=excluded.caller_jid`, o.id, o.remote.String(), o.media, o.at.UTC().Format("2006-01-02 15:04:05Z07:00"))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("call identity conflict")
	}
	return nil
}
