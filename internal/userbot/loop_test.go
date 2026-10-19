package userbot

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
	"github.com/ozodjongg/secretar_ai_agent/internal/miniapp"
	"github.com/ozodjongg/secretar_ai_agent/internal/ratelimit"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

func loopAudits(t *testing.T, e *env) int {
	t.Helper()
	n, _ := e.st.CountAudit(context.Background(), owner, []string{"loop_guard"}, time.Time{})
	return n
}

func TestLoopGuardStopsLongAutoStreak(t *testing.T) {
	e := newEnv(t, auto)
	var notes []string
	e.p.Notify = func(_ context.Context, _ int64, text string, _ bool) { notes = append(notes, text) }
	for i := 0; i < loopMaxAuto+3; i++ {
		e.p.Handle(context.Background(), in("salom", true))
	}
	if got := e.tg.sentCount(); got != loopMaxAuto {
		t.Fatalf("ketma-ket %d ta avto javob kutilgan, yuborildi: %d", loopMaxAuto, got)
	}
	ds := e.drafts()
	if len(ds) != 3 || !strings.Contains(ds[0].Reason, "sikl") {
		t.Fatalf("chegaradan keyin qoralama kutilgan: %+v", ds)
	}
	if loopAudits(t, e) != 3 {
		t.Fatal("loop_guard auditga yozilishi kerak")
	}
	loops := 0
	for _, n := range notes {
		if strings.Contains(n, "🔁") {
			loops++
		}
	}
	if loops != 1 {
		t.Fatalf("sikl haqida bitta ogohlantirish kutilgan, %d: %v", loops, notes)
	}
}

func TestLoopGuardResetsWhenOwnerWrites(t *testing.T) {
	e := newEnv(t, auto)
	for i := 0; i < loopMaxAuto; i++ {
		e.p.Handle(context.Background(), in("salom", true))
	}
	e.p.OwnerMessage(context.Background(), 100, "o'zim yozdim")
	e.p.Handle(context.Background(), in("salom", true))
	if got := e.tg.sentCount(); got != loopMaxAuto+1 {
		t.Fatalf("egasi yozgach avto javob davom etishi kerak, yuborildi: %d", got)
	}
}

func TestLoopGuardNotResetByOwnEcho(t *testing.T) {
	e := newEnv(t, auto)
	for i := 0; i < loopMaxAuto; i++ {
		e.p.Handle(context.Background(), in("salom", true))
		// Kotib yuborgan xabar Telegram'dan "out" bo'lib qaytadi — bu egasining aralashuvi emas.
		e.p.OwnerMessage(context.Background(), 100, e.llm.reply)
	}
	e.p.Handle(context.Background(), in("salom", true))
	if got := e.tg.sentCount(); got != loopMaxAuto {
		t.Fatalf("kotibning o'z xabari hisobni nolga tushirmasligi kerak, yuborildi: %d", got)
	}
}

func TestLoopGuardResetsAfterDraftApproved(t *testing.T) {
	e := newEnv(t, auto)
	for i := 0; i < loopMaxAuto+1; i++ {
		e.p.Handle(context.Background(), in("salom", true))
	}
	ds := e.drafts()
	if len(ds) != 1 {
		t.Fatalf("qoralama kutilgan: %d", len(ds))
	}
	if err := e.p.SendDraft(context.Background(), ds[0]); err != nil {
		t.Fatal(err)
	}
	e.p.Handle(context.Background(), in("salom", true))
	if got := e.tg.sentCount(); got != loopMaxAuto+2 {
		t.Fatalf("egasi tasdiqlagach avto javob davom etishi kerak, yuborildi: %d", got)
	}
}

func TestLoopGuardWindowExpires(t *testing.T) {
	e := newEnv(t, auto)
	now := e.p.Now()
	var mu sync.Mutex
	e.p.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	for i := 0; i < loopMaxAuto+1; i++ {
		e.p.Handle(context.Background(), in("salom", true))
	}
	mu.Lock()
	now = now.Add(loopWindow + time.Minute)
	mu.Unlock()
	e.p.Handle(context.Background(), in("salom", true))
	if got := e.tg.sentCount(); got != loopMaxAuto+1 {
		t.Fatalf("oyna o'tgach avto javob qayta ruxsat etilishi kerak, yuborildi: %d", got)
	}
}

func TestLoopGuardStricterForPeerSecretary(t *testing.T) {
	e := newEnv(t, auto)
	e.p.PeerSecretary = func(id int64) bool { return id == 100 }
	for i := 0; i < 5; i++ {
		e.p.Handle(context.Background(), in("salom", true))
	}
	if got := e.tg.sentCount(); got != loopMaxPeer {
		t.Fatalf("ikkinchi kotibga %d ta avto javob kutilgan, yuborildi: %d", loopMaxPeer, got)
	}
}

func TestLoopGuardPerChat(t *testing.T) {
	e := newEnv(t, auto)
	for i := 0; i < loopMaxAuto+1; i++ {
		e.p.Handle(context.Background(), in("salom", true))
	}
	other := Incoming{ChatID: 200, MsgID: 1, Text: "salom", Contact: config.Contact{UserID: 200, IsContact: true}}
	e.p.Handle(context.Background(), other)
	if got := e.tg.sentCount(); got != loopMaxAuto+1 {
		t.Fatalf("boshqa chatga ta'sir qilmasligi kerak, yuborildi: %d", got)
	}
}

// bridgeTG — ikki kotibni bir-biriga ulaydi: yuborilgan xabar qarshi tomonga kiruvchi bo'lib boradi.
type bridgeTG struct {
	mu    sync.Mutex
	self  int64
	queue *[]Incoming
	sent  int
}

func (b *bridgeTG) SendReply(_ context.Context, _ int64, _ int, text string, _ time.Duration) error {
	b.mu.Lock()
	b.sent++
	b.mu.Unlock()
	*b.queue = append(*b.queue, Incoming{ChatID: b.self, MsgID: 1, Text: text,
		Contact: config.Contact{UserID: b.self, IsContact: true}})
	return nil
}
func (b *bridgeTG) MarkRead(context.Context, int64, int) error                      { return nil }
func (b *bridgeTG) History(context.Context, int64, int) ([]llm.Turn, error)         { return nil, nil }
func (b *bridgeTG) LogOut(context.Context) error                                    { return nil }
func (b *bridgeTG) Contacts(context.Context) ([]miniapp.ContactInfo, error)         { return nil, nil }
func (b *bridgeTG) RecentChats(context.Context, int) ([]miniapp.ContactInfo, error) { return nil, nil }

// TestTwoAutoSecretariesDoNotLoop — ikki egasi ham avto rejimda: kotiblar bir-biriga
// cheksiz javob yozmasligi kerak.
func TestTwoAutoSecretariesDoNotLoop(t *testing.T) {
	for _, peerKnown := range []bool{false, true} {
		const a, b = int64(11), int64(22)
		var toA, toB []Incoming
		mk := func(self, peer int64, out *[]Incoming) (*Pipeline, *bridgeTG) {
			st := store.NewMemory()
			s := config.Default()
			s.Mode = config.ModeAuto
			_ = st.SaveSettings(context.Background(), self, s)
			tg := &bridgeTG{self: self, queue: out}
			p := &Pipeline{
				Owner: self, Store: st, LLM: &fakeLLM{reply: "Rahmat, xabaringizni oldim!"}, TG: tg,
				Limiter: ratelimit.NewLimiter(ratelimit.Config{GlobalBurst: 1000, GlobalPerSec: 1000, ChatBurst: 1000, ChatPerSec: 1000, DailyMax: 1000}),
				Sleep:   func(context.Context, time.Duration) error { return nil },
			}
			if peerKnown {
				p.PeerSecretary = func(id int64) bool { return id == peer }
			}
			return p, tg
		}
		pa, ta := mk(a, b, &toB) // A yuborgani B'ga boradi
		pb, tb := mk(b, a, &toA)
		toB = append(toB, Incoming{ChatID: a, MsgID: 1, Text: "Salom!", Contact: config.Contact{UserID: a, IsContact: true}})
		for step := 0; step < 200 && (len(toA) > 0 || len(toB) > 0); step++ {
			if len(toB) > 0 {
				m := toB[0]
				toB = toB[1:]
				pb.Handle(context.Background(), m)
			}
			if len(toA) > 0 {
				m := toA[0]
				toA = toA[1:]
				pa.Handle(context.Background(), m)
			}
		}
		if len(toA) > 0 || len(toB) > 0 {
			t.Fatalf("peerKnown=%v: kotiblar o'zaro sikldan chiqmadi (A=%d, B=%d)", peerKnown, ta.sent, tb.sent)
		}
		limit := loopMaxAuto
		if peerKnown {
			limit = loopMaxPeer
		}
		if ta.sent > limit || tb.sent > limit {
			t.Fatalf("peerKnown=%v: juda ko'p avto javob: A=%d, B=%d", peerKnown, ta.sent, tb.sent)
		}
	}
}
