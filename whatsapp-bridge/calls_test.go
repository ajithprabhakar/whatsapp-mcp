package main

import (
	"database/sql"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"testing"
	"time"
)

func callStore(t *testing.T) *MessageStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err = ensureCallsTable(db); err != nil {
		t.Fatal(err)
	}
	return &MessageStore{db: db}
}
func jid(user string) types.JID { return types.NewJID(user, types.DefaultUserServer) }
func TestCallLifecyclePersistsOnceAndPreservesSummary(t *testing.T) {
	s := callStore(t)
	own, remote := jid("15550000001"), jid("15550000002")
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	meta := types.BasicCallMeta{CallID: "fixture-call", From: remote, CallCreator: remote, Timestamp: at}
	offer := &events.CallOffer{BasicCallMeta: meta, Data: &waBinary.Node{Content: []waBinary.Node{{Tag: "audio"}}}}
	handled, err := recordCallEvent(s, offer, &own, types.EmptyJID)
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	s.db.Exec("UPDATE calls SET summary='owner summary' WHERE call_id=?", meta.CallID)
	meta.Timestamp = at.Add(time.Minute)
	for _, evt := range []any{&events.CallAccept{BasicCallMeta: meta}, &events.CallTerminate{BasicCallMeta: meta}, offer} {
		if _, err := recordCallEvent(s, evt, &own, types.EmptyJID); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	var media, summary, partner string
	if err := s.db.QueryRow("SELECT count(*),call_type,summary,caller_jid FROM calls").Scan(&count, &media, &summary, &partner); err != nil {
		t.Fatal(err)
	}
	if count != 1 || media != "voice" || summary != "owner summary" || partner != remote.String() {
		t.Fatalf("unexpected persisted observation")
	}
}
func TestOutgoingAndGroupBoundaries(t *testing.T) {
	s := callStore(t)
	own, remote := jid("15550000001"), jid("15550000002")
	meta := types.BasicCallMeta{CallID: "outgoing", CallCreator: own, From: remote, Timestamp: time.Now()}
	if _, err := recordCallEvent(s, &events.CallAccept{BasicCallMeta: meta}, &own, types.EmptyJID); err != nil {
		t.Fatal(err)
	}
	var partner, media string
	s.db.QueryRow("SELECT caller_jid,call_type FROM calls WHERE call_id='outgoing'").Scan(&partner, &media)
	if partner != remote.String() || media != "unknown" {
		t.Fatal("outgoing partner/media incorrectly inferred")
	}
	meta.CallID = "group"
	meta.GroupJID = types.NewJID("fixture", types.GroupServer)
	recordCallEvent(s, &events.CallOffer{BasicCallMeta: meta}, &own, types.EmptyJID)
	var n int
	s.db.QueryRow("SELECT count(*) FROM calls").Scan(&n)
	if n != 1 {
		t.Fatal("group admitted")
	}
	handled, _ := recordCallEvent(s, &events.Message{}, &own, types.EmptyJID)
	if handled {
		t.Fatal("message intercepted")
	}
}
func TestLIDAlternativeAndSelfFailClosed(t *testing.T) {
	s := callStore(t)
	own := jid("15550000001")
	lid := types.NewJID("1234567890123456", types.HiddenUserServer)
	remote := jid("15550000002")
	meta := types.BasicCallMeta{CallID: "lid", From: lid, CallCreator: lid, CallCreatorAlt: remote, Timestamp: time.Now()}
	recordCallEvent(s, &events.CallOffer{BasicCallMeta: meta, Data: &waBinary.Node{Content: []waBinary.Node{{Tag: "video"}}}}, &own, types.EmptyJID)
	var partner, media string
	s.db.QueryRow("SELECT caller_jid,call_type FROM calls").Scan(&partner, &media)
	if partner != remote.String() || media != "video" {
		t.Fatal("observed PN/video lost")
	}
	meta.CallID = "self"
	meta.CallCreator = own
	meta.CallCreatorAlt = types.EmptyJID
	meta.From = own
	recordCallEvent(s, &events.CallTerminate{BasicCallMeta: meta}, &own, types.EmptyJID)
	var n int
	s.db.QueryRow("SELECT count(*) FROM calls").Scan(&n)
	if n != 1 {
		t.Fatal("self admitted")
	}
}

func TestOutOfOrderMediaUpgradeAndKnownPartnerConflict(t *testing.T) {
	s := callStore(t)
	own, remote := jid("15550000001"), jid("15550000002")
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	meta := types.BasicCallMeta{CallID: "late-offer", From: remote, CallCreator: remote, Timestamp: at.Add(time.Minute)}
	recordCallEvent(s, &events.CallTerminate{BasicCallMeta: meta}, &own, types.EmptyJID)
	meta.Timestamp = at
	recordCallEvent(s, &events.CallOffer{BasicCallMeta: meta, Data: &waBinary.Node{Content: []waBinary.Node{{Tag: "video"}}}}, &own, types.EmptyJID)
	var media string
	var observed time.Time
	if err := s.db.QueryRow("SELECT call_type,timestamp FROM calls").Scan(&media, &observed); err != nil {
		t.Fatal(err)
	}
	if media != "video" || !observed.Equal(at) {
		t.Fatal("known media/earliest observation lost")
	}
	meta.CallCreator = jid("15550000003")
	meta.From = meta.CallCreator
	if _, err := recordCallEvent(s, &events.CallReject{BasicCallMeta: meta}, &own, types.EmptyJID); err == nil {
		t.Fatal("identity conflict silently accepted")
	}
	if err := ensureCallsTable(s.db); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM calls").Scan(&n)
	if n != 1 {
		t.Fatal("dedup/initializer changed rows")
	}
}
func TestMissingOwnerCreatorOrTimestampDoesNotInventCall(t *testing.T) {
	s := callStore(t)
	own, remote := jid("15550000001"), jid("15550000002")
	valid := types.BasicCallMeta{CallID: "fixture", From: remote, CallCreator: remote, Timestamp: time.Now()}
	recordCallEvent(s, &events.CallOffer{BasicCallMeta: valid}, nil, types.EmptyJID)
	invalid := valid
	invalid.CallCreator = types.EmptyJID
	recordCallEvent(s, &events.CallAccept{BasicCallMeta: invalid}, &own, types.EmptyJID)
	invalid = valid
	invalid.Timestamp = time.Time{}
	recordCallEvent(s, &events.CallReject{BasicCallMeta: invalid}, &own, types.EmptyJID)
	recordCallEvent(s, &events.CallOfferNotice{BasicCallMeta: valid, Type: "group", Media: "video"}, &own, types.EmptyJID)
	var n int
	s.db.QueryRow("SELECT count(*) FROM calls").Scan(&n)
	if n != 0 {
		t.Fatal("untrusted observation admitted")
	}
}
func TestOwnerLIDAndUnresolvedRemoteLIDAreNotPhoneNumbers(t *testing.T) {
	s := callStore(t)
	own := jid("15550000001")
	ownLID := types.NewJID("7777777777777777", types.HiddenUserServer)
	remoteLID := types.NewJID("8888888888888888", types.HiddenUserServer)
	meta := types.BasicCallMeta{CallID: "out-lid", From: remoteLID, CallCreator: ownLID, Timestamp: time.Now()}
	recordCallEvent(s, &events.CallTerminate{BasicCallMeta: meta}, &own, ownLID)
	var partner string
	s.db.QueryRow("SELECT caller_jid FROM calls").Scan(&partner)
	if partner != remoteLID.String() {
		t.Fatal("LID must retain its identity namespace")
	}
}
