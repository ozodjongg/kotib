package botcmd

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

func linkToken(t *testing.T, st *store.Memory, token string, parent int64, exp time.Time) {
	t.Helper()
	if err := st.SaveLinkToken(context.Background(), store.TokenHash(token), store.LinkToken{ParentID: parent, Label: "Ish", ExpiresAt: exp}); err != nil {
		t.Fatal(err)
	}
}

func TestLinkConsentFlow(t *testing.T) {
	h, st, ctl, notes := setup(t)
	ctx := context.Background()
	linkToken(t, st, "tok1", 1, time.Now().Add(30*time.Minute))

	// Asosiy akkaunt o'z havolasini ochsa — rad.
	if r := h.Command(ctx, 1, "start", "link_tok1"); len(r.Buttons) != 0 || !strings.Contains(r.Text, "boshqa Telegram") {
		t.Fatalf("o'zi: %+v", r)
	}
	// Qo'shiladigan akkaunt: rozilik so'rovi.
	r := h.Command(ctx, 5, "start", "link_tok1")
	if len(r.Buttons) != 2 || r.Buttons[0].Data != "lnk:tok1:y" || !strings.Contains(r.Text, "Ish") || !strings.Contains(r.Text, "rad eting") {
		t.Fatalf("rozilik: %+v", r)
	}
	a := h.Callback(ctx, 5, "lnk:tok1:y")
	if !strings.Contains(a.Edit, "Tasdiqlandi") {
		t.Fatalf("tasdiq: %+v", a)
	}
	if l, err := st.GetLink(ctx, 5); err != nil || l.ParentID != 1 || l.Label != "Ish" {
		t.Fatalf("link: %+v %v", l, err)
	}
	if acc, err := st.GetAccount(ctx, 5); err != nil || acc.ConsentAt == nil || acc.Status != store.StatusPending {
		t.Fatalf("akkaunt: %+v %v", acc, err)
	}
	if len(*notes) != 1 || !strings.Contains((*notes)[0], "rozilik berdi") {
		t.Fatalf("asosiyga xabar: %v", *notes)
	}
	// Token bir martalik.
	if a := h.Callback(ctx, 6, "lnk:tok1:y"); !strings.Contains(a.Edit, "eskirgan") {
		t.Fatalf("takror: %+v", a)
	}

	// Qoralama qo'shimcha akkauntda — tugma asosiy akkaunt chatidan ishlaydi.
	id, _ := st.CreateDraft(ctx, store.Draft{OwnerID: 5, ChatID: 9, Incoming: "salom", Reply: "va alaykum", Status: store.DraftPending})
	if ans := h.Callback(ctx, 1, "d:"+itoa(id)+":s"); ans.Toast != "Yuborildi" || len(ctl.sent) != 1 {
		t.Fatalf("qoralama: %+v %v", ans, ctl.sent)
	}
	// Begona akkaunt qoralamasi topilmaydi.
	id2, _ := st.CreateDraft(ctx, store.Draft{OwnerID: 77, ChatID: 9, Incoming: "x", Reply: "y", Status: store.DraftPending})
	if ans := h.Callback(ctx, 1, "d:"+itoa(id2)+":r"); ans.Toast != "Topilmadi" {
		t.Fatalf("begona: %+v", ans)
	}

	// Xabar yo'naltirish.
	if chat, text := Route(ctx, st, 5, "salom"); chat != 1 || !strings.HasPrefix(text, "👤 Ish") {
		t.Fatalf("route: %d %q", chat, text)
	}
	if chat, text := Route(ctx, st, 1, "salom"); chat != 1 || text != "salom" {
		t.Fatal("asosiy akkaunt o'zgarmaydi")
	}
	if r := h.Command(ctx, 1, "status", ""); !strings.Contains(r.Text, "👤 Ish") {
		t.Fatalf("status: %s", r.Text)
	}
}

func TestLinkRefusals(t *testing.T) {
	h, st, _, _ := setup(t)
	ctx := context.Background()
	linkToken(t, st, "old", 1, time.Now().Add(-time.Minute))
	if r := h.Command(ctx, 5, "start", "link_old"); !strings.Contains(r.Text, "eskirgan") {
		t.Fatal(r.Text)
	}
	if r := h.Command(ctx, 5, "start", "link_nope"); !strings.Contains(r.Text, "eskirgan") {
		t.Fatal(r.Text)
	}
	linkToken(t, st, "tok", 1, time.Now().Add(time.Hour))
	// Kotib'da alohida ishlatilayotgan akkaunt.
	now := time.Now()
	_ = st.UpsertAccount(ctx, store.Account{OwnerID: 6, Status: store.StatusActive, ConsentAt: &now})
	if r := h.Command(ctx, 6, "start", "link_tok"); !strings.Contains(r.Text, "alohida") {
		t.Fatal(r.Text)
	}
	// Chegara.
	for i := int64(0); i < billing.MaxLinkedAccounts; i++ {
		_, _ = st.AddLink(ctx, store.Link{ChildID: 100 + i, ParentID: 1})
	}
	if r := h.Command(ctx, 7, "start", "link_tok"); !strings.Contains(r.Text, "Pro") {
		t.Fatal(r.Text)
	}
	// Rad etish tokenni o'chiradi.
	linkToken(t, st, "tok2", 2, time.Now().Add(time.Hour))
	_ = st.UpsertAccount(ctx, store.Account{OwnerID: 2, Status: store.StatusActive, ConsentAt: &now})
	if a := h.Callback(ctx, 8, "lnk:tok2:n"); !strings.Contains(a.Edit, "rad") {
		t.Fatalf("rad: %+v", a)
	}
	if _, err := st.GetLinkToken(ctx, store.TokenHash("tok2")); err == nil {
		t.Fatal("token o'chirilishi kerak")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
