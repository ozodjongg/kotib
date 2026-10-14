package botcmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

type fakeCtl struct{ paused, resumed int }

func (f *fakeCtl) Pause(int64)                         { f.paused++ }
func (f *fakeCtl) Resume(context.Context, int64) error { f.resumed++; return nil }

type fakeAPI struct{}

func (fakeAPI) CreateInvoiceLink(context.Context, billing.InvoiceLink) (string, error) {
	return "https://t.me/$x", nil
}
func (fakeAPI) EditUserStarSubscription(context.Context, int64, string, bool) error { return nil }
func (fakeAPI) RefundStarPayment(context.Context, int64, string) error              { return nil }

func setup(t *testing.T) (*Handler, *store.Memory, *fakeCtl, *[]string) {
	t.Helper()
	st := store.NewMemory()
	now := time.Now()
	_ = st.UpsertAccount(context.Background(), store.Account{OwnerID: 1, Status: store.StatusActive, ConsentAt: &now})
	ctl := &fakeCtl{}
	var notes []string
	h := &Handler{
		Store: st, Ctl: ctl, Admins: map[int64]bool{99: true},
		Billing: &billing.Service{Store: st, API: fakeAPI{}, Price: 50, TrialDays: 3},
		Notify:  func(_ context.Context, _ int64, text string, _ bool) { notes = append(notes, text) },
	}
	return h, st, ctl, &notes
}

func TestAdminCommandsOnlyForAdmins(t *testing.T) {
	h, _, _, _ := setup(t)
	ctx := context.Background()
	if r := h.Command(ctx, 1, "stats", ""); !strings.Contains(r.Text, "Noma'lum") {
		t.Fatalf("oddiy foydalanuvchi /stats ko'rmasligi kerak: %q", r.Text)
	}
	if r := h.Command(ctx, 1, "refund", "ch1"); !strings.Contains(r.Text, "Noma'lum") {
		t.Fatal("oddiy foydalanuvchi /refund qila olmasligi kerak")
	}
	if r := h.Command(ctx, 99, "stats", ""); !strings.Contains(r.Text, "Akkauntlar: 1") {
		t.Fatalf("admin stats: %q", r.Text)
	}
}

func TestPauseResume(t *testing.T) {
	h, st, ctl, _ := setup(t)
	ctx := context.Background()
	// sinov muddati tugagan mijoz
	_ = st.SaveSubscription(ctx, store.Subscription{OwnerID: 1, TrialUntil: time.Now().Add(-time.Hour)})
	h.Command(ctx, 1, "pause", "")
	if a, _ := st.GetAccount(ctx, 1); a.Status != store.StatusPaused || ctl.paused != 1 {
		t.Fatal("pause")
	}
	r := h.Command(ctx, 1, "resume", "")
	if a, _ := st.GetAccount(ctx, 1); a.Status != store.StatusActive || ctl.resumed != 1 {
		t.Fatal("resume")
	}
	if !strings.Contains(r.Text, "obuna faol emas") {
		t.Fatalf("obunasiz ogohlantirish kerak: %q", r.Text)
	}
	if r := h.Command(ctx, 2, "resume", ""); ctl.resumed != 1 || !r.OpenApp {
		t.Fatal("ro'yxatdan o'tmagan foydalanuvchi resume qila olmasligi kerak")
	}
}

func TestSubscribeAndPaid(t *testing.T) {
	h, _, _, notes := setup(t)
	ctx := context.Background()
	r := h.Command(ctx, 1, "obuna", "")
	if r.ButtonURL != "https://t.me/$x" || !strings.Contains(r.Button, "50") {
		t.Fatalf("obuna tugmasi: %+v", r)
	}
	h.Paid(ctx, 1, billing.PaymentInfo{Currency: "XTR", Amount: 50, ChargeID: "c1"})
	if len(*notes) != 1 || !strings.Contains((*notes)[0], "rahmat") {
		t.Fatalf("to'lov xabari: %v", *notes)
	}
	if r := h.Command(ctx, 1, "obuna", ""); r.ButtonURL != "" {
		t.Fatal("faol obunada qayta to'lov tugmasi bo'lmasligi kerak")
	}
	if r := h.Command(ctx, 1, "status", ""); !strings.Contains(r.Text, "keyingi to'lov") {
		t.Fatalf("status: %q", r.Text)
	}
}

type fakeExec struct {
	n   int
	err error
}

func (f *fakeExec) Execute(context.Context, store.Action) (string, error) {
	f.n++
	return "Google Calendar'ga qo'shildi ✅", f.err
}

func TestActionCallback(t *testing.T) {
	h, st, _, _ := setup(t)
	ctx := context.Background()
	ex := &fakeExec{}
	h.Google = ex
	payload := []byte(`{"title":"Aziz bilan uchrashuv","start":"2026-10-15T15:00:00+05:00","tz":"Asia/Tashkent","duration_min":60}`)
	id, _ := st.CreateAction(ctx, store.Action{OwnerID: 1, Kind: store.ActionEvent, Payload: payload})
	data := ActionButtons(id)[0].Data

	if toast, _ := h.Callback(ctx, 2, data); !strings.Contains(toast, "Topilmadi") || ex.n != 0 {
		t.Fatal("boshqa foydalanuvchi bosa olmasligi kerak")
	}
	_, edit := h.Callback(ctx, 1, data)
	if ex.n != 1 || !strings.Contains(edit, "Aziz bilan uchrashuv") || !strings.Contains(edit, "qo'shildi") || strings.Contains(edit, "qo'shaymi") {
		t.Fatalf("n=%d edit=%q", ex.n, edit)
	}
	if toast, _ := h.Callback(ctx, 1, data); !strings.Contains(toast, "Allaqachon") || ex.n != 1 {
		t.Fatal("ikki marta bajarilmasligi kerak")
	}

	id2, _ := st.CreateAction(ctx, store.Action{OwnerID: 1, Kind: store.ActionEvent, Payload: payload})
	if _, edit := h.Callback(ctx, 1, ActionButtons(id2)[1].Data); !strings.Contains(edit, "Qo'shilmadi") || ex.n != 1 {
		t.Fatalf("rad etish: %q", edit)
	}

	ex.err = errors.New("x")
	id3, _ := st.CreateAction(ctx, store.Action{OwnerID: 1, Kind: store.ActionEvent, Payload: payload})
	if _, edit := h.Callback(ctx, 1, ActionButtons(id3)[0].Data); !strings.Contains(edit, "⚠️") {
		t.Fatalf("xato: %q", edit)
	}
	if a, _ := st.GetAction(ctx, 1, id3); a.Status != store.ActionFailed {
		t.Fatalf("holat: %s", a.Status)
	}
	for _, bad := range []string{"act:x:y", "act:1:z", "boshqa"} {
		if toast, edit := h.Callback(ctx, 1, bad); toast != "" || edit != "" {
			t.Fatalf("%q qabul qilinmasligi kerak", bad)
		}
	}
}
