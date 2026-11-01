package main

import (
	"database/sql"
	"fmt"
	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	"strings"
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

func TestHistoryCallActualHandler(t *testing.T) {
	s := callStore(t)
	own, remote := jid("15550000001"), jid("15550000002")
	client := whatsmeow.NewClient(&store.Device{ID: &own}, waLog.Noop)
	r := &waSyncAction.CallLogRecord{CallID: proto.String("history-fixture"), StartTime: proto.Int64(time.Now().Add(-time.Hour).Unix()), IsIncoming: proto.Bool(false), IsVideo: proto.Bool(true), CallCreatorJID: proto.String(own.String()), Participants: []*waSyncAction.CallLogRecord_ParticipantInfo{{UserJID: proto.String(remote.String())}}}
	evt := &events.HistorySync{Data: &waHistorySync.HistorySync{CallLogRecords: []*waSyncAction.CallLogRecord{r}}}
	handleHistorySync(client, s, evt, waLog.Noop)
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM calls").Scan(&n); err != nil || n != 1 {
		t.Fatalf("history call count=%d err=%v", n, err)
	}
	if _, err := s.db.Exec("UPDATE calls SET summary='owner summary'"); err != nil {
		t.Fatal(err)
	}
	r.IsVideo = proto.Bool(false)
	r.StartTime = proto.Int64(time.Now().Unix())
	handleHistorySync(client, s, evt, waLog.Noop)
	var summary, media, partner string
	if err := s.db.QueryRow("SELECT summary,call_type,caller_jid FROM calls").Scan(&summary, &media, &partner); err != nil {
		t.Fatal(err)
	}
	if summary != "owner summary" || media != "video" || partner != remote.String() {
		t.Fatal("history identity/media/summary lost")
	}
	var observed time.Time
	if err := s.db.QueryRow("SELECT count(*),timestamp FROM calls").Scan(&n, &observed); err != nil || n != 1 || !observed.Before(time.Now().Add(-30*time.Minute)) {
		t.Fatalf("history replay changed identity/time count=%d err=%v", n, err)
	}
}

func TestHistoryCallValidationAndBatchContinues(t *testing.T) {
	own, remote := jid("15550000001"), jid("15550000002")
	ownLID := types.NewJID("7777777777777777", types.HiddenUserServer)
	base := &waSyncAction.CallLogRecord{CallID: proto.String("valid"), StartTime: proto.Int64(time.Now().Add(-time.Hour).Unix()), IsIncoming: proto.Bool(true), CallCreatorJID: proto.String(remote.String()), Participants: []*waSyncAction.CallLogRecord_ParticipantInfo{{UserJID: proto.String(own.String())}, {UserJID: proto.String(remote.String())}, {UserJID: proto.String(remote.String())}, {UserJID: proto.String(ownLID.String())}}}
	cases := []struct {
		name   string
		mutate func(*waSyncAction.CallLogRecord)
	}{
		{"missing-id", func(r *waSyncAction.CallLogRecord) { r.CallID = nil }},
		{"blank-id", func(r *waSyncAction.CallLogRecord) { r.CallID = proto.String("  ") }},
		{"direction", func(r *waSyncAction.CallLogRecord) { r.IsIncoming = nil }},
		{"zero-time", func(r *waSyncAction.CallLogRecord) { r.StartTime = proto.Int64(0) }},
		{"negative-time", func(r *waSyncAction.CallLogRecord) { r.StartTime = proto.Int64(-1) }},
		{"pre-era", func(r *waSyncAction.CallLogRecord) { r.StartTime = proto.Int64(1) }},
		{"ns-time", func(r *waSyncAction.CallLogRecord) { r.StartTime = proto.Int64(time.Now().UnixNano()) }},
		{"overflow-time", func(r *waSyncAction.CallLogRecord) { r.StartTime = proto.Int64(9223372036854775807) }},
		{"future-time", func(r *waSyncAction.CallLogRecord) { r.StartTime = proto.Int64(time.Now().Add(time.Hour).Unix()) }},
		{"future-six-min", func(r *waSyncAction.CallLogRecord) { r.StartTime = proto.Int64(time.Now().Add(6 * time.Minute).Unix()) }},
		{"group", func(r *waSyncAction.CallLogRecord) { r.GroupJID = proto.String("fixture@g.us") }},
		{"link", func(r *waSyncAction.CallLogRecord) { r.IsCallLink = proto.Bool(true) }},
		{"nonregular", func(r *waSyncAction.CallLogRecord) { r.CallType = waSyncAction.CallLogRecord_VOICE_CHAT.Enum() }},
		{"creator", func(r *waSyncAction.CallLogRecord) { r.CallCreatorJID = proto.String("bad@g.us") }},
		{"missing-creator", func(r *waSyncAction.CallLogRecord) { r.CallCreatorJID = nil }},
		{"outgoing-remote-creator", func(r *waSyncAction.CallLogRecord) { r.IsIncoming = proto.Bool(false) }},
		{"self", func(r *waSyncAction.CallLogRecord) { r.CallCreatorJID = proto.String(own.String()) }},
		{"ambiguous", func(r *waSyncAction.CallLogRecord) {
			r.Participants = append(r.Participants, &waSyncAction.CallLogRecord_ParticipantInfo{UserJID: proto.String(jid("15550000003").String())})
		}},
		{"nil-participant", func(r *waSyncAction.CallLogRecord) { r.Participants = append(r.Participants, nil) }},
		{"invalid-participant", func(r *waSyncAction.CallLogRecord) {
			r.Participants = append(r.Participants, &waSyncAction.CallLogRecord_ParticipantInfo{UserJID: proto.String("bad@g.us")})
		}},
		{"mixed-remote-namespaces", func(r *waSyncAction.CallLogRecord) {
			r.Participants = append(r.Participants, &waSyncAction.CallLogRecord_ParticipantInfo{UserJID: proto.String("8888888888888888@lid")})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := callStore(t)
			client := whatsmeow.NewClient(&store.Device{ID: &own, LID: ownLID}, waLog.Noop)
			bad := proto.Clone(base).(*waSyncAction.CallLogRecord)
			bad.CallID = proto.String("invalid")
			tc.mutate(bad)
			handleHistorySync(client, s, &events.HistorySync{Data: &waHistorySync.HistorySync{CallLogRecords: []*waSyncAction.CallLogRecord{nil, bad, base}}}, waLog.Noop)
			var n int
			if err := s.db.QueryRow("SELECT count(*) FROM calls").Scan(&n); err != nil || n != 1 {
				t.Fatalf("count=%d err=%v", n, err)
			}
		})
	}
	t.Run("identity-conflict-continues", func(t *testing.T) {
		s := callStore(t)
		client := whatsmeow.NewClient(&store.Device{ID: &own, LID: ownLID}, waLog.Noop)
		if err := s.storeCallObservation(callObservation{"conflict", jid("15550000003"), "video", time.Now()}); err != nil {
			t.Fatal(err)
		}
		bad := proto.Clone(base).(*waSyncAction.CallLogRecord)
		bad.CallID = proto.String("conflict")
		l := &callLogCapture{Logger: waLog.Noop}
		handleHistorySync(client, s, &events.HistorySync{Data: &waHistorySync.HistorySync{CallLogRecords: []*waSyncAction.CallLogRecord{bad, base}}}, l)
		log := strings.Join(l.lines, "\n")
		if len(l.errors) != 1 || l.errors[0] != "History calls reason=persistence_failed count=1" {
			t.Fatalf("persistence failure not reported at ERROR: %v", l.errors)
		}
		if !strings.Contains(log, "persistence_failed:1") || strings.Contains(log, "conflict") || strings.Contains(log, remote.User) || strings.Contains(log, "call identity") {
			t.Fatalf("unsafe or missing persistence diagnostics: %s", log)
		}
		var n int
		s.db.QueryRow("SELECT count(*) FROM calls").Scan(&n)
		if n != 2 {
			t.Fatalf("batch stopped after conflict: %d", n)
		}
	})
	t.Run("missing-owner", func(t *testing.T) {
		s := callStore(t)
		client := whatsmeow.NewClient(&store.Device{}, waLog.Noop)
		handleHistorySync(client, s, &events.HistorySync{Data: &waHistorySync.HistorySync{CallLogRecords: []*waSyncAction.CallLogRecord{base}}}, waLog.Noop)
		var n int
		s.db.QueryRow("SELECT count(*) FROM calls").Scan(&n)
		if n != 0 {
			t.Fatal("missing owner accepted")
		}
	})
}

type callLogCapture struct {
	waLog.Logger
	lines  []string
	errors []string
}

func (l *callLogCapture) Infof(f string, a ...any) { l.lines = append(l.lines, fmt.Sprintf(f, a...)) }
func (l *callLogCapture) Errorf(f string, a ...any) {
	line := fmt.Sprintf(f, a...)
	l.lines = append(l.lines, line)
	l.errors = append(l.errors, line)
}

func TestHistoryCallNamespacesMediaAndDiagnostics(t *testing.T) {
	own := jid("15550000001")
	ownLID := types.NewJID("7777777777777777", types.HiddenUserServer)
	for _, video := range []*bool{nil, proto.Bool(false), proto.Bool(true)} {
		t.Run(fmt.Sprint(video), func(t *testing.T) {
			s := callStore(t)
			client := whatsmeow.NewClient(&store.Device{ID: &own, LID: ownLID}, waLog.Noop)
			remote := "8888888888888888@lid"
			r := &waSyncAction.CallLogRecord{CallID: proto.String("private-call-id"), StartTime: proto.Int64(time.Now().Add(4 * time.Minute).Unix()), IsIncoming: proto.Bool(false), IsVideo: video, CallCreatorJID: proto.String(ownLID.String()), Participants: []*waSyncAction.CallLogRecord_ParticipantInfo{{UserJID: proto.String(remote)}}}
			l := &callLogCapture{Logger: waLog.Noop}
			handleHistorySync(client, s, &events.HistorySync{Data: &waHistorySync.HistorySync{CallLogRecords: []*waSyncAction.CallLogRecord{nil, r}}}, l)
			var partner, media string
			if err := s.db.QueryRow("SELECT caller_jid,call_type FROM calls").Scan(&partner, &media); err != nil {
				t.Fatal(err)
			}
			want := "unknown"
			if video != nil {
				want = "voice"
				if *video {
					want = "video"
				}
			}
			if partner != remote || media != want {
				t.Fatal("namespace/media not preserved")
			}
			log := strings.Join(l.lines, "\n")
			for _, secret := range []string{remote, own.String(), ownLID.String(), r.GetCallID(), "15550000001", "8888888888888888"} {
				if strings.Contains(log, secret) {
					t.Fatal("private call data logged")
				}
			}
			if !strings.Contains(log, "seen=2 stored=1") || !strings.Contains(log, "invalid_record:1") {
				t.Fatalf("missing sanitized counts: %s", log)
			}
			if len(l.errors) != 0 {
				t.Fatalf("validation skip reported as storage error: %v", l.errors)
			}
		})
	}
	for _, invalid := range []string{"", "bad", "123@broadcast", "123@newsletter", "123@bot", "123:2@s.whatsapp.net", "abc@s.whatsapp.net"} {
		r := &waSyncAction.CallLogRecord{CallID: proto.String("invalid"), StartTime: proto.Int64(time.Now().Unix()), IsIncoming: proto.Bool(true), CallCreatorJID: proto.String(invalid)}
		if _, reason := historyCallObservation(r, &own, ownLID, time.Now()); reason == "" {
			t.Fatalf("invalid creator accepted: %q", invalid)
		}
		r.CallCreatorJID = proto.String(jid("15550000002").String())
		r.Participants = []*waSyncAction.CallLogRecord_ParticipantInfo{{UserJID: proto.String(invalid)}}
		if _, reason := historyCallObservation(r, &own, ownLID, time.Now()); reason == "" {
			t.Fatalf("invalid participant accepted: %q", invalid)
		}
	}
}

func TestHistoryCallTimestampUnitsActualHandler(t *testing.T) {
	own, remote := jid("15550000001"), jid("15550000002")
	at := time.Date(2026, 10, 1, 12, 34, 56, 123456000, time.UTC)
	for _, raw := range []int64{at.Unix(), at.UnixMilli(), at.UnixMicro()} {
		t.Run(fmt.Sprint(raw), func(t *testing.T) {
			s := callStore(t)
			client := whatsmeow.NewClient(&store.Device{ID: &own}, waLog.Noop)
			r := &waSyncAction.CallLogRecord{CallID: proto.String("timestamp-fixture"), StartTime: proto.Int64(raw), IsIncoming: proto.Bool(true), CallCreatorJID: proto.String(remote.String())}
			handleHistorySync(client, s, &events.HistorySync{Data: &waHistorySync.HistorySync{CallLogRecords: []*waSyncAction.CallLogRecord{r}}}, waLog.Noop)
			var observed time.Time
			if err := s.db.QueryRow("SELECT timestamp FROM calls").Scan(&observed); err != nil {
				t.Fatal(err)
			}
			if !observed.Equal(at.Truncate(time.Second)) {
				t.Fatalf("timestamp unit changed instant: %v", observed)
			}
		})
	}
}
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
