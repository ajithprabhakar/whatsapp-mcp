package main

import (
	"database/sql"
	"fmt"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"strings"
	"time"
	"unicode"
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

func historyCallObservation(r *waSyncAction.CallLogRecord, ownPN *types.JID, ownLID types.JID, now time.Time) (callObservation, string) {
	invalid := func(reason string) (callObservation, string) { return callObservation{}, reason }
	if r == nil {
		return invalid("invalid_record")
	}
	if ownPN == nil || ownPN.IsEmpty() {
		return invalid("missing_owner")
	}
	id := r.GetCallID()
	if id == "" || strings.TrimSpace(id) != id || strings.ContainsFunc(id, unicode.IsControl) {
		return invalid("invalid_id")
	}
	ts := r.GetStartTime()
	if ts <= 0 {
		return invalid("invalid_time")
	}
	var at time.Time
	switch {
	case ts < 100_000_000_000:
		at = time.Unix(ts, 0)
	case ts < 100_000_000_000_000:
		at = time.UnixMilli(ts)
	default:
		at = time.UnixMicro(ts)
	}
	if at.Before(time.Unix(1230768000, 0)) || at.After(now.Add(5*time.Minute)) {
		return invalid("invalid_time")
	}
	if r.IsIncoming == nil {
		return invalid("missing_direction")
	}
	if r.GetGroupJID() != "" || r.GetIsCallLink() || r.GetCallLinkToken() != "" || r.GetCallType() != waSyncAction.CallLogRecord_REGULAR {
		return invalid("group_or_link")
	}
	parse := func(raw string) (types.JID, bool) {
		j, err := types.ParseJID(raw)
		if err != nil || j.IsEmpty() || j != j.ToNonAD() || (j.Server != types.DefaultUserServer && j.Server != types.HiddenUserServer) {
			return types.EmptyJID, false
		}
		for _, digit := range j.User {
			if digit < '0' || digit > '9' {
				return types.EmptyJID, false
			}
		}
		return j, true
	}
	isOwn := func(j types.JID) bool { return j == ownPN.ToNonAD() || (!ownLID.IsEmpty() && j == ownLID.ToNonAD()) }
	creator, ok := parse(r.GetCallCreatorJID())
	if !ok {
		return invalid("invalid_creator")
	}
	if r.GetIsIncoming() == isOwn(creator) {
		return invalid("direction_conflict")
	}
	remote := types.EmptyJID
	if !isOwn(creator) {
		remote = creator
	}
	for _, participant := range r.GetParticipants() {
		j, valid := parse(participant.GetUserJID())
		if !valid {
			return invalid("invalid_participant")
		}
		if isOwn(j) {
			continue
		}
		if !remote.IsEmpty() && remote != j {
			return invalid("ambiguous_partner")
		}
		remote = j
	}
	if remote.IsEmpty() {
		return invalid("missing_partner")
	}
	media := "unknown"
	if r.IsVideo != nil {
		media = "voice"
		if r.GetIsVideo() {
			media = "video"
		}
	}
	return callObservation{id, remote, media, at}, ""
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
