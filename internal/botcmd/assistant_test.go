package botcmd

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/bot"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
	"github.com/ozodjongg/secretar_ai_agent/internal/userbot"
)

type fakeAI struct {
	mu      sync.Mutex
	asked   []int64 // qaysi akkauntlarda qidirildi
	answers map[int64]string
	err     error
	choices []string
}

func (f *fakeAI) Ask(_ context.Context, owner int64, q string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, owner)
	if a, ok := f.answers[owner]; ok {
		return a + " / " + q, nil
	}
	if f.err != nil {
		return "", f.err
	}
	return "", userbot.ErrNoContact
}

func (f *fakeAI) ApplyShadowChoice(_ context.Context, owner int64, c string) (string, error) {
	f.choices = append(f.choices, fmt.Sprintf("%d:%s", owner, c))
	return "ok " + c, nil
}

// waitNotes — fondagi javobni kutadi.
func waitNotes(t *testing.T, mu *sync.Mutex, notes *[]string, n int) []string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		got := append([]string(nil), *notes...)
		mu.Unlock()
		if len(got) >= n {
			return got
		}
		select {
		case <-deadline:
			t.Fatalf("javob kelmadi: %v", got)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestAskCommand(t *testing.T) {
	h, st, _, _ := setup(t)
	var mu sync.Mutex
	var notes []string
	h.Notify = func(_ context.Context, _ int64, text string, _ bool) {
		mu.Lock()
		notes = append(notes, text)
		mu.Unlock()
	}
	ctx := context.Background()
	if r := h.Command(ctx, 1, "sor", ""); !strings.Contains(r.Text, "Disabled") && !strings.Contains(r.Text, "o'chiq") {
		t.Fatalf("AI'siz: %q", r.Text)
	}
	ai := &fakeAI{answers: map[int64]string{2: "💬 Aziz\n\nertaga 15:00"}}
	h.AI = ai
	if r := h.Command(ctx, 1, "sor", ""); !strings.Contains(r.Text, "Aziz bilan") {
		t.Fatalf("yo'riqnoma: %q", r.Text)
	}
	// Asosiy akkauntda topilmadi — qo'shimcha akkauntda (2) topildi.
	_, _ = st.AddLink(ctx, store.Link{ChildID: 2, ParentID: 1, Label: "Ish"})
	if r := h.Command(ctx, 1, "sor", "Aziz bilan nima kelishdik?"); !strings.Contains(r.Text, "o'qiyapman") {
		t.Fatalf("darhol javob: %q", r.Text)
	}
	got := waitNotes(t, &mu, &notes, 1)
	if !strings.Contains(got[0], "ertaga 15:00 / Aziz bilan nima kelishdik?") || len(ai.asked) != 2 || ai.asked[0] != 1 {
		t.Fatalf("javob: %v asked=%v", got, ai.asked)
	}
	// Pro emas.
	h.AI = &fakeAI{err: userbot.ErrNeedPro}
	h.Command(ctx, 1, "sor", "Aziz?")
	if got := waitNotes(t, &mu, &notes, 2); !strings.Contains(got[1], "Pro") {
		t.Fatalf("pro: %v", got)
	}
	// Kontakt topilmadi.
	h.AI = &fakeAI{}
	h.Command(ctx, 1, "sor", "ob-havo?")
	if got := waitNotes(t, &mu, &notes, 3); !strings.Contains(got[2], "kontakt ismini topmadim") {
		t.Fatalf("kontaktsiz: %v", got)
	}
}

func TestShadowCallback(t *testing.T) {
	h, st, _, _ := setup(t)
	ctx := context.Background()
	ai := &fakeAI{}
	h.AI = ai
	_, _ = st.AddLink(ctx, store.Link{ChildID: 2, ParentID: 1})
	if a := h.Callback(ctx, 1, userbot.ShadowAuto+":2"); a.Toast != "Saqlandi" || a.Edit != "ok "+userbot.ShadowAuto {
		t.Fatalf("%+v", a)
	}
	if a := h.Callback(ctx, 5, userbot.ShadowAuto+":1"); a.Toast != "Topilmadi" {
		t.Fatalf("begona akkaunt: %+v", a)
	}
	if a := h.Callback(ctx, 1, "shd:boshqa:1"); a.Toast != "" {
		t.Fatalf("noma'lum: %+v", a)
	}
	if len(ai.choices) != 1 || ai.choices[0] != "2:"+userbot.ShadowAuto {
		t.Fatalf("%v", ai.choices)
	}
}

type fakeAgent struct {
	mu    sync.Mutex
	allow bool
	got   []string
	err   error
}

func (f *fakeAgent) Allow(int64) bool { return f.allow }
func (f *fakeAgent) Handle(_ context.Context, user int64, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, text)
	return "Bajarildi: " + text, f.err
}

func TestFreeTextGoesToAgent(t *testing.T) {
	h, _, _, _ := setup(t)
	var mu sync.Mutex
	var notes []string
	h.Notify = func(_ context.Context, _ int64, text string, _ bool) {
		mu.Lock()
		notes = append(notes, text)
		mu.Unlock()
	}
	ctx := context.Background()
	// Agent yo'q — oddiy matn avvalgidek /sor savoli.
	h.AI = &fakeAI{}
	if r := h.Command(ctx, 1, bot.TextCommand, "Aziz bilan nima kelishdik?"); !strings.Contains(r.Text, "o'qiyapman") {
		t.Fatalf("agentsiz: %q", r.Text)
	}
	waitNotes(t, &mu, &notes, 1)

	ag := &fakeAgent{allow: true}
	h.Agent = ag
	typing := make(chan int64, 10)
	h.Typing = func(_ context.Context, chat int64) error { typing <- chat; return nil }
	if r := h.Command(ctx, 1, bot.TextCommand, "avto rejimni yoq"); r.Text != "" {
		t.Fatalf("darhol xabar bo'lmasligi kerak: %q", r.Text)
	}
	if got := waitNotes(t, &mu, &notes, 2); got[1] != "Bajarildi: avto rejimni yoq" {
		t.Fatalf("%v", got)
	}
	if c := <-typing; c != 1 {
		t.Fatal("typing")
	}
	ag.allow = false
	if r := h.Command(ctx, 1, bot.TextCommand, "yana"); !strings.Contains(r.Text, "kuting") {
		t.Fatalf("limit: %q", r.Text)
	}
	// Bloklangan foydalanuvchi agentga yetmaydi.
	ag.allow = true
	_ = h.Store.SetBan(ctx, store.Ban{OwnerID: 1, Reason: "x"})
	if r := h.Command(ctx, 1, bot.TextCommand, "salom"); r.Text != BannedText {
		t.Fatalf("ban: %q", r.Text)
	}
}
