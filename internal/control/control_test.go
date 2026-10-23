package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/crypto"
	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
	"github.com/ozodjongg/secretar_ai_agent/internal/miniapp"
	"github.com/ozodjongg/secretar_ai_agent/internal/plugin"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

const user = int64(7)

// fakeCtl — miniapp.Controller (Telegram sessiyasisiz).
type fakeCtl struct{ paused bool }

func (f *fakeCtl) BeginLogin(context.Context, int64, string) error { return nil }
func (f *fakeCtl) CancelLogin(int64)                               {}
func (f *fakeCtl) SubmitCode(int64, string) error                  { return nil }
func (f *fakeCtl) SubmitPassword(int64, string) error              { return nil }
func (f *fakeCtl) LoginState(int64) miniapp.LoginState             { return miniapp.LoginState{} }
func (f *fakeCtl) Pause(int64)                                     { f.paused = true }
func (f *fakeCtl) Resume(context.Context, int64) error             { return nil }
func (f *fakeCtl) Revoke(context.Context, int64) error             { return nil }
func (f *fakeCtl) SendDraft(context.Context, int64, store.Draft) error {
	return nil
}
func (f *fakeCtl) Contacts(context.Context, int64) ([]miniapp.ContactInfo, error) {
	return []miniapp.ContactInfo{{ID: 100, Name: "Aziz Karimov"}, {ID: 101, Name: "Onam"}}, nil
}
func (f *fakeCtl) StartLearnStyle(context.Context, int64) error { return nil }

type fakeSched struct{}

func (fakeSched) RunNow(context.Context, int64, int64) error { return nil }

type stubSearch struct{}

func (stubSearch) WebSearch(context.Context, string) (string, error) { return "", nil }

// step — soxta model chaqiradigan vosita.
type step struct {
	name string
	args string // JSON
}

// scriptLLM — vositalarni berilgan tartibda chaqiradi va natijalarni yakuniy matnga yozadi.
type scriptLLM struct {
	steps []step
	last  llm.ControlRequest
}

func (s *scriptLLM) Control(ctx context.Context, r llm.ControlRequest) (llm.TaskResult, error) {
	s.last = r
	var res llm.TaskResult
	var out []string
	for _, st := range s.steps {
		var args map[string]any
		_ = json.Unmarshal([]byte(st.args), &args)
		got, err := r.Call(ctx, st.name, args)
		if err != nil {
			got = "XATO: " + err.Error()
		}
		res.Tools = append(res.Tools, st.name)
		out = append(out, got)
	}
	res.Text = strings.Join(out, "\n")
	return res, nil
}

func setup(t *testing.T) (*Agent, *scriptLLM, *store.Memory, *fakeCtl) {
	t.Helper()
	st := store.NewMemory()
	ctx := context.Background()
	now := time.Now()
	_ = st.UpsertAccount(ctx, store.Account{OwnerID: user, Status: store.StatusActive, ConsentAt: &now})
	_ = st.SaveSettings(ctx, user, config.Default())
	keys, err := crypto.NewStaticKeyFromBase64("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	ctl := &fakeCtl{}
	api := &miniapp.Server{BotToken: "T", Store: st, Ctl: ctl, Schedules: fakeSched{},
		Plugins: plugin.Default(plugin.Options{Search: stubSearch{}}), Vault: crypto.NewVault(keys)}
	m := &scriptLLM{}
	return &Agent{API: api, LLM: m, Store: st}, m, st, ctl
}

func TestUpdateSettingsMerges(t *testing.T) {
	a, m, st, _ := setup(t)
	ctx := context.Background()
	m.steps = []step{{"update_settings", `{"changes_json":"{\"mode\":\"auto\",\"quiet_hours\":{\"start\":\"22:00\"},\"contact_notes\":{\"100\":\"hamkor\"},\"style_score\":99}"}`}}
	out, err := a.Handle(ctx, user, "avto rejimni yoq, tinch soatlar 22 dan")
	if err != nil || !strings.Contains(out, "saqlandi") {
		t.Fatalf("%q %v", out, err)
	}
	s, _ := st.GetSettings(ctx, user)
	if s.Mode != config.ModeAuto || s.Quiet.Start != "22:00" || s.Quiet.End != "07:00" || !s.Quiet.Enabled {
		t.Fatalf("birlashtirilmadi: %+v", s.Quiet)
	}
	if s.ContactNotes[100] != "hamkor" || s.StyleScore != 0 {
		t.Fatalf("eslatma/server maydoni: %v %d", s.ContactNotes, s.StyleScore)
	}
	// Yaroqsiz qiymat — server xatosi modelga qaytadi, hech narsa saqlanmaydi.
	m.steps = []step{{"update_settings", `{"changes_json":"{\"mode\":\"yomon\"}"}`}}
	if out, _ := a.Handle(ctx, user, "x"); !strings.Contains(out, "XATO") {
		t.Fatalf("validatsiya: %q", out)
	}
	if s, _ := st.GetSettings(ctx, user); s.Mode != config.ModeAuto {
		t.Fatal("yaroqsiz sozlama saqlanmasligi kerak")
	}
}

func TestAwayPauseDraftsAndContacts(t *testing.T) {
	a, m, st, ctl := setup(t)
	ctx := context.Background()
	id, _ := st.CreateDraft(ctx, store.Draft{OwnerID: user, ChatID: 100, Incoming: "qalaysan", Reply: "yaxshi"})
	m.steps = []step{
		{"set_away", `{"text":"rulda","minutes":60}`},
		{"find_contacts", `{"query":"aziz"}`},
		{"send_draft", `{"id":` + jsonInt(id) + `,"text":"zo'r, o'zing-chi?"}`},
		{"pause", `{}`},
	}
	out, err := a.Handle(ctx, user, "...")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := st.GetSettings(ctx, user)
	if s.Away != "rulda" || s.AwayUntil == nil {
		t.Fatalf("away: %+v", s.Away)
	}
	if !strings.Contains(out, `"Aziz Karimov"`) || strings.Contains(out, "Onam") {
		t.Fatalf("kontaktlar: %s", out)
	}
	if d, _ := st.GetDraft(ctx, user, id); d.Status != store.DraftSent {
		t.Fatalf("qoralama: %v", d.Status)
	}
	if !ctl.paused {
		t.Fatal("pause")
	}
	if n, _ := st.CountAudit(ctx, user, []string{"bot_control"}, time.Time{}); n != 1 {
		t.Fatalf("audit: %d", n)
	}
}

func TestSchedules(t *testing.T) {
	a, m, st, _ := setup(t)
	ctx := context.Background()
	m.steps = []step{{"create_schedule", `{"prompt":"ob-havo","kind":"weekly","at":"08:00","weekdays":[1,7]}`}}
	if out, _ := a.Handle(ctx, user, "x"); strings.Contains(out, "XATO") {
		t.Fatal(out)
	}
	list, _ := st.ListSchedules(ctx, user)
	if len(list) != 1 || list[0].Weekdays != 1<<1|1<<0 || list[0].At != "08:00" {
		t.Fatalf("%+v", list)
	}
	id := jsonInt(list[0].ID)
	m.steps = []step{{"update_schedule", `{"id":` + id + `,"at":"09:30","enabled":false}`}}
	if out, _ := a.Handle(ctx, user, "x"); strings.Contains(out, "XATO") {
		t.Fatal(out)
	}
	sc, _ := st.GetSchedule(ctx, user, list[0].ID)
	if sc.At != "09:30" || sc.Enabled || sc.Prompt != "ob-havo" || sc.Weekdays != list[0].Weekdays {
		t.Fatalf("o'zgarmagan maydonlar saqlanishi kerak: %+v", sc)
	}
	m.steps = []step{{"delete_schedule", `{"id":` + id + `}`}}
	a.Handle(ctx, user, "x")
	if list, _ := st.ListSchedules(ctx, user); len(list) != 0 {
		t.Fatal("o'chmadi")
	}
}

func TestPluginsSecretsStayInApp(t *testing.T) {
	a, m, _, _ := setup(t)
	ctx := context.Background()
	m.steps = []step{
		{"save_plugin", `{"id":"weather","enabled":true,"config_json":"{\"city\":\"Samarqand\"}"}`},
		{"list_plugins", `{}`},
		{"save_plugin", `{"id":"notion","enabled":true,"config_json":"{\"token\":\"ntn_secret\"}"}`},
	}
	out, _ := a.Handle(ctx, user, "x")
	if !strings.Contains(out, `"city":"Samarqand"`) {
		t.Fatalf("weather: %s", out)
	}
	if !strings.Contains(out, "faqat ilovada") || strings.Contains(out, "ntn_secret") {
		t.Fatalf("secret: %s", out)
	}
}

func TestAppOnlyAndHistory(t *testing.T) {
	a, m, st, _ := setup(t)
	ctx := context.Background()
	m.steps = []step{{"nonexistent", `{}`}, {"use_account", `{"id":555}`}}
	out, _ := a.Handle(ctx, user, "akkauntimni o'chir")
	if !strings.Contains(out, "bunday vosita yo'q") || !strings.Contains(out, "bunday akkaunt yo'q") {
		t.Fatal(out)
	}
	if _, err := st.GetAccount(ctx, user); err != nil {
		t.Fatal("akkaunt o'chmasligi kerak")
	}
	m.steps = nil
	a.Handle(ctx, user, "ikkinchi")
	if h := m.last.History; len(h) != 2 || !h[0].FromOwner || h[0].Text != "akkauntimni o'chir" {
		t.Fatalf("tarix: %+v", h)
	}
	if len(m.last.Tools) < 30 {
		t.Fatalf("vositalar: %d", len(m.last.Tools))
	}
}

func TestRateLimit(t *testing.T) {
	a, _, _, _ := setup(t)
	n := 0
	for i := 0; i < 20; i++ {
		if a.Allow(user) {
			n++
		}
	}
	if n != 10 {
		t.Fatalf("ruxsat: %d", n)
	}
}

func TestIsoDays(t *testing.T) {
	if m, err := isoDays([]any{1.0, 5.0}); err != nil || m != 1<<1|1<<5 {
		t.Fatal(m, err)
	}
	if _, err := isoDays([]any{0.0}); err == nil {
		t.Fatal("0 yaroqsiz")
	}
}

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }
