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
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

func TestIsPromise(t *testing.T) {
	yes := []string{
		"Hozir ish bilan bandman, keyinroq yozaman.", "O’zim yozaman", "Maqul, aniqlab yozaman",
		"Сейчас занят, позже напишу", "I'll get back to you soon",
	}
	no := []string{"Yaxshi, rahmat!", "Ha, ertaga ko'rishamiz", "Привет, как дела?", "Sure, see you"}
	for _, s := range yes {
		if !IsPromise(s) {
			t.Errorf("va'da deb topilishi kerak: %q", s)
		}
	}
	for _, s := range no {
		if IsPromise(s) {
			t.Errorf("va'da emas: %q", s)
		}
	}
	if textHash("Keyinroq  yozaman.") != textHash("keyinroq yozaman.") {
		t.Fatal("xesh bo'sh joy va registrga bog'liq bo'lmasligi kerak")
	}
}

func TestPromiseTrackedOnSend(t *testing.T) {
	e := newEnv(t, auto)
	_ = e.st.UpsertAccount(context.Background(), store.Account{OwnerID: owner, Status: store.StatusActive})
	e.llm.reply = "Hozir bandman, keyinroq yozaman."
	e.p.Handle(context.Background(), in("salom", true))
	fs, _ := e.st.ListOpenFollowups(context.Background(), owner)
	if len(fs) != 1 || fs[0].ChatID != 100 || !fs[0].DueAt.Equal(e.p.Now().Add(followupDelay)) {
		t.Fatalf("avto yuborilgan va'da kuzatilishi kerak: %+v", fs)
	}
	// Tasdiqlangan qoralama ham.
	_ = e.p.SendDraft(context.Background(), store.Draft{ChatID: 200, Reply: "Keyin yozaman"})
	if fs, _ := e.st.ListOpenFollowups(context.Background(), owner); len(fs) != 2 {
		t.Fatalf("qoralama va'dasi ham: %+v", fs)
	}
	// Oddiy javob — kuzatilmaydi; sozlamada o'chirilsa — ham.
	_ = e.p.SendDraft(context.Background(), store.Draft{ChatID: 300, Reply: "Rahmat!"})
	st, _ := e.st.GetSettings(context.Background(), owner)
	st.NoFollowups = true
	_ = e.st.SaveSettings(context.Background(), owner, st)
	_ = e.p.SendDraft(context.Background(), store.Draft{ChatID: 400, Reply: "keyinroq yozaman"})
	if fs, _ := e.st.ListOpenFollowups(context.Background(), owner); len(fs) != 2 {
		t.Fatalf("faqat ikkita va'da bo'lishi kerak: %+v", fs)
	}
}

// histTG — History oldindan berilgan xabarlarni qaytaradi.
type histTG struct {
	fakeTG
	hist []llm.Turn
}

func (h *histTG) History(context.Context, int64, int) ([]llm.Turn, error) { return h.hist, nil }
func (h *histTG) Contacts(context.Context) ([]miniapp.ContactInfo, error) {
	return []miniapp.ContactInfo{{ID: 100, Name: "Shovkat aka"}}, nil
}

// readyConn — Run darhol onReady(tg) chaqiradi va ctx tugaguncha ishlaydi.
type readyConn struct {
	fakeConn
	tg Telegram
}

func (c *readyConn) Run(ctx context.Context, _ int64, onReady func(Telegram), _ func(Incoming)) error {
	onReady(c.tg)
	<-ctx.Done()
	return ctx.Err()
}

func runningManager(t *testing.T, tg Telegram) (*Manager, *store.Memory, *[]string) {
	t.Helper()
	m, st := newManager(&readyConn{tg: tg})
	var mu sync.Mutex
	var notes []string
	m.Notify = func(_ context.Context, _ int64, text string, _ bool) {
		mu.Lock()
		notes = append(notes, text)
		mu.Unlock()
	}
	now := time.Now()
	_ = st.UpsertAccount(context.Background(), store.Account{OwnerID: owner, Status: store.StatusActive, ConsentAt: &now})
	_ = st.SaveSettings(context.Background(), owner, config.Default())
	m.startWorker(owner)
	deadline := time.After(2 * time.Second)
	for {
		m.mu.Lock()
		w := m.workers[owner]
		m.mu.Unlock()
		if w != nil && w.telegram() != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("worker ulanmadi")
		case <-time.After(5 * time.Millisecond):
		}
	}
	t.Cleanup(m.Shutdown)
	return m, st, &notes
}

func TestCheckFollowups(t *testing.T) {
	promise := "Hozir bandman, keyinroq yozaman."
	tg := &histTG{hist: []llm.Turn{{FromOwner: false, Text: "salom"}, {FromOwner: true, Text: promise}, {FromOwner: false, Text: "ok"}}}
	m, st, notes := runningManager(t, tg)
	ctx := context.Background()
	now := time.Now()
	_ = st.SaveFollowup(ctx, store.Followup{OwnerID: owner, ChatID: 100, Hash: textHash(promise), DueAt: now.Add(-time.Minute)})
	m.CheckFollowups(ctx, now)
	if len(*notes) != 1 || !strings.Contains((*notes)[0], "Shovkat aka") {
		t.Fatalf("eslatma: %v", *notes)
	}
	if fs, _ := st.ListOpenFollowups(ctx, owner); len(fs) != 1 || fs[0].Status != store.FollowupReminded {
		t.Fatalf("holat reminded bo'lishi kerak: %+v", fs)
	}
	m.CheckFollowups(ctx, now)
	if len(*notes) != 1 {
		t.Fatal("ikki marta eslatilmasligi kerak")
	}
	// Egasi o'zi yozgan bo'lsa — eslatilmaydi, yopiladi.
	tg.hist = append(tg.hist, llm.Turn{FromOwner: true, Text: "Uzr, hozir qo'ng'iroq qilaman"})
	_ = st.SaveFollowup(ctx, store.Followup{OwnerID: owner, ChatID: 100, Hash: textHash(promise), DueAt: now.Add(-time.Minute)})
	m.CheckFollowups(ctx, now)
	if len(*notes) != 1 {
		t.Fatalf("egasi yozgan — eslatma bo'lmasligi kerak: %v", *notes)
	}
	if fs, _ := st.ListOpenFollowups(ctx, owner); len(fs) != 0 {
		t.Fatalf("yopilishi kerak: %+v", fs)
	}
}

func TestSendDigests(t *testing.T) {
	m, st, notes := runningManager(t, &histTG{})
	ctx := context.Background()
	loc, _ := time.LoadLocation("Asia/Tashkent")
	evening := time.Date(2026, 10, 14, 21, 10, 0, 0, loc)
	morning := time.Date(2026, 10, 14, 10, 0, 0, 0, loc)

	// Faoliyat yo'q — xulosa yuborilmaydi.
	m.SendDigests(ctx, evening)
	if len(*notes) != 0 {
		t.Fatalf("faoliyatsiz xulosa: %v", *notes)
	}
	st.Audit(ctx, owner, "draft_created", "")
	st.Audit(ctx, owner, "auto_sent", "")
	st.Audit(ctx, owner, "auto_sent", "")
	_, _ = st.CreateDraft(ctx, store.Draft{OwnerID: owner, ChatID: 100, Incoming: "x", Reply: "y"})
	_ = st.SaveFollowup(ctx, store.Followup{OwnerID: owner, ChatID: 100, Hash: "h", DueAt: time.Now()})

	m.SendDigests(ctx, morning) // soat 21 emas
	if len(*notes) != 0 {
		t.Fatal("ertalab xulosa yuborilmasligi kerak")
	}
	m.SendDigests(ctx, evening)
	if len(*notes) != 1 {
		t.Fatalf("xulosa: %v", *notes)
	}
	d := (*notes)[0]
	for _, want := range []string{"Tayyorlangan javoblar: 3", "Yuborilgan: 2", "qoralamalar: 1", "Shovkat aka", "2 daqiqa"} {
		if !strings.Contains(d, want) {
			t.Errorf("xulosada %q yo'q:\n%s", want, d)
		}
	}
	m.SendDigests(ctx, evening.Add(20*time.Minute))
	if len(*notes) != 1 {
		t.Fatal("kuniga bir marta")
	}
}

func TestDigestDisabled(t *testing.T) {
	m, st, notes := runningManager(t, &histTG{})
	ctx := context.Background()
	s, _ := st.GetSettings(ctx, owner)
	s.NoDigest = true
	_ = st.SaveSettings(ctx, owner, s)
	st.Audit(ctx, owner, "auto_sent", "")
	loc, _ := time.LoadLocation("Asia/Tashkent")
	m.SendDigests(ctx, time.Date(2026, 10, 14, 21, 5, 0, 0, loc))
	if len(*notes) != 0 {
		t.Fatal("o'chirilgan xulosa yuborilmasligi kerak")
	}
}
