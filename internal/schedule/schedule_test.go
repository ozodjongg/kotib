package schedule

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
	"github.com/ozodjongg/secretar_ai_agent/internal/plugin"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

var tash, _ = time.LoadLocation("Asia/Tashkent")

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, tash)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNext(t *testing.T) {
	now := at("2026-10-16 09:30") // juma
	cases := []struct {
		s    store.Schedule
		want string
	}{
		{store.Schedule{Kind: store.ScheduleDaily, At: "10:00"}, "2026-10-16 10:00"},
		{store.Schedule{Kind: store.ScheduleDaily, At: "09:30"}, "2026-10-17 09:30"}, // aynan hozir — keyingisi
		{store.Schedule{Kind: store.ScheduleDaily, At: "08:00"}, "2026-10-17 08:00"},
		{store.Schedule{Kind: store.ScheduleHourly, At: "00:45"}, "2026-10-16 09:45"},
		{store.Schedule{Kind: store.ScheduleHourly, At: "00:15"}, "2026-10-16 10:15"},
		{store.Schedule{Kind: store.ScheduleWeekdays, At: "08:00"}, "2026-10-19 08:00"}, // shanba-yakshanba o'tkaziladi
		{store.Schedule{Kind: store.ScheduleWeekdays, At: "18:00"}, "2026-10-16 18:00"},
		{store.Schedule{Kind: store.ScheduleWeekly, At: "07:00", Weekdays: 1 << time.Wednesday}, "2026-10-21 07:00"},
		{store.Schedule{Kind: store.ScheduleWeekly, At: "12:00", Weekdays: 1<<time.Friday | 1<<time.Monday}, "2026-10-16 12:00"},
		{store.Schedule{Kind: store.ScheduleOnce, At: "11:00", Date: "2026-10-20"}, "2026-10-20 11:00"},
	}
	for _, c := range cases {
		got := Next(c.s, now, tash)
		if got == nil || got.In(tash).Format("2006-01-02 15:04") != c.want {
			t.Errorf("%s %s: %v, kutilgan %s", c.s.Kind, c.s.At, got, c.want)
		}
	}
	if Next(store.Schedule{Kind: store.ScheduleOnce, At: "09:00", Date: "2026-10-16"}, now, tash) != nil {
		t.Fatal("o'tgan bir martalik vaqt")
	}
	if Next(store.Schedule{Kind: store.ScheduleDaily, At: "25:00"}, now, tash) != nil {
		t.Fatal("noto'g'ri vaqt")
	}
	// Yozgi vaqtga o'tish (Berlin, 2026-03-29 02:00 → 03:00): har kuni 09:00 mahalliy vaqtda qoladi.
	ber, _ := time.LoadLocation("Europe/Berlin")
	n := Next(store.Schedule{Kind: store.ScheduleDaily, At: "09:00"}, time.Date(2026, 3, 28, 10, 0, 0, 0, ber), ber)
	if n.In(ber).Format("2006-01-02 15:04") != "2026-03-29 09:00" {
		t.Fatalf("DST: %v", n.In(ber))
	}
}

func TestNormalizeAndDescribe(t *testing.T) {
	s := store.Schedule{Prompt: "  Har kuni   ob-havoni ayt  ", Kind: store.ScheduleWeekly, At: "07:30", Weekdays: 1<<1 | 1<<5 | 1<<0, Date: "x"}
	if err := Normalize(&s); err != nil || s.Title != "Har kuni ob-havoni ayt" || s.Date != "" || s.Prompt != "Har kuni   ob-havoni ayt" {
		t.Fatalf("%+v %v", s, err)
	}
	if d := Describe(s); d != "Du, Ju, Ya 07:30" {
		t.Fatalf("describe: %q", d)
	}
	long := store.Schedule{Prompt: strings.Repeat("a ", 100), Kind: store.ScheduleDaily, At: "09:00"}
	if err := Normalize(&long); err != nil || len([]rune(long.Title)) != MaxTitle {
		t.Fatalf("uzun nom: %d %v", len([]rune(long.Title)), err)
	}
	bad := []store.Schedule{
		{Prompt: "", Kind: store.ScheduleDaily, At: "09:00"},
		{Prompt: "x", Kind: store.ScheduleDaily, At: "9:00"},
		{Prompt: "x", Kind: store.ScheduleDaily, At: "24:00"},
		{Prompt: "x", Kind: "monthly", At: "09:00"},
		{Prompt: "x", Kind: store.ScheduleWeekly, At: "09:00"},
		{Prompt: "x", Kind: store.ScheduleWeekly, At: "09:00", Weekdays: 128},
		{Prompt: "x", Kind: store.ScheduleOnce, At: "09:00", Date: "2026-13-01"},
		{Prompt: strings.Repeat("x", MaxPrompt+1), Kind: store.ScheduleDaily, At: "09:00"},
	}
	for _, b := range bad {
		var e *ErrInvalid
		if err := Normalize(&b); !errors.As(err, &e) {
			t.Errorf("rad etilishi kerak: %+v (%v)", b, err)
		}
	}
	once := store.Schedule{Prompt: "x", Kind: store.ScheduleOnce, At: "09:00", Date: "2026-10-16", Enabled: true}
	if err := Plan(&once, at("2026-10-16 10:00"), tash); err == nil {
		t.Fatal("o'tgan vaqt rad etilishi kerak")
	}
	once.Enabled = false
	if err := Plan(&once, at("2026-10-16 10:00"), tash); err != nil || once.NextRun != nil {
		t.Fatal("o'chiq vazifa rejalashtirilmaydi")
	}
}

// ---- Runner ----

type fakeAI struct {
	mu   sync.Mutex
	reqs []llm.TaskRequest
	err  error
}

func (f *fakeAI) RunTask(ctx context.Context, r llm.TaskRequest) (llm.TaskResult, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	f.mu.Unlock()
	if f.err != nil {
		return llm.TaskResult{}, f.err
	}
	out := "Natija: " + r.Prompt
	if r.Call != nil && len(r.Tools) > 0 {
		w, err := r.Call(ctx, r.Tools[0].Name, map[string]any{"query": "q"})
		if err != nil {
			return llm.TaskResult{}, err
		}
		out += " | " + w
	}
	return llm.TaskResult{Text: out}, nil
}

func (f *fakeAI) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.reqs) }

type plainVault struct{}

func (plainVault) Seal(p, aad []byte) ([]byte, error) {
	return append(append([]byte{}, aad...), p...), nil
}
func (plainVault) Open(b, aad []byte) ([]byte, error) {
	if !strings.HasPrefix(string(b), string(aad)) {
		return nil, errors.New("aad")
	}
	return b[len(aad):], nil
}

type notes struct {
	mu   sync.Mutex
	msgs []string
}

func (n *notes) add(_ context.Context, _ int64, text string) {
	n.mu.Lock()
	n.msgs = append(n.msgs, text)
	n.mu.Unlock()
}
func (n *notes) all() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.msgs...)
}

type searchStub struct{}

func (searchStub) WebSearch(_ context.Context, q string) (string, error) { return "web:" + q, nil }

func newRunner(t *testing.T) (*Runner, *store.Memory, *fakeAI, *notes, *time.Time) {
	t.Helper()
	st := store.NewMemory()
	ctx := context.Background()
	now := at("2026-10-16 09:00")
	_ = st.UpsertAccount(ctx, store.Account{OwnerID: 7, Status: store.StatusActive, ConsentAt: &now})
	cfg := config.Default()
	cfg.OwnerName = "Shavkat"
	cfg.Quiet.TZ = "Asia/Tashkent"
	_ = st.SaveSettings(ctx, 7, cfg)
	ai, n := &fakeAI{}, &notes{}
	clock := now
	r := &Runner{Store: st, AI: ai, Notify: n.add, Vault: plainVault{},
		Plugins: plugin.Default(plugin.Options{Search: searchStub{}}), Now: func() time.Time { return clock }}
	return r, st, ai, n, &clock
}

func create(t *testing.T, st store.Store, s store.Schedule, now time.Time) int64 {
	t.Helper()
	s.OwnerID, s.Enabled = 7, true
	if err := Normalize(&s); err != nil {
		t.Fatal(err)
	}
	if err := Plan(&s, now, tash); err != nil {
		t.Fatal(err)
	}
	id, err := st.CreateSchedule(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRunnerTick(t *testing.T) {
	r, st, ai, n, clock := newRunner(t)
	ctx := context.Background()
	daily := create(t, st, store.Schedule{Title: "Ob-havo", Prompt: "bugungi ob-havo", Kind: store.ScheduleDaily, At: "09:30"}, *clock)
	once := create(t, st, store.Schedule{Prompt: "dollar kursi", Kind: store.ScheduleOnce, At: "09:30", Date: "2026-10-16"}, *clock)

	r.Tick(ctx)
	r.Wait()
	if ai.count() != 0 {
		t.Fatal("muddati kelmagan vazifa bajarilmasligi kerak")
	}
	*clock = at("2026-10-16 09:30")
	r.Tick(ctx)
	r.Tick(ctx) // ikkinchi tick ikki marta bajarmasin
	r.Wait()
	if ai.count() != 2 {
		t.Fatalf("ikkala vazifa bir martadan: %d", ai.count())
	}
	msgs := n.all()
	if len(msgs) != 2 || !strings.Contains(strings.Join(msgs, "\n"), "⏰ Ob-havo\n\nNatija: bugungi ob-havo") {
		t.Fatalf("xabarlar: %q", msgs)
	}
	if req := ai.reqs[0]; req.OwnerName != "Shavkat" || req.Now.Location().String() != "Asia/Tashkent" || len(req.Tools) != 0 {
		t.Fatalf("so'rov: %+v", req)
	}
	d, _ := st.GetSchedule(ctx, 7, daily)
	if d.LastStatus != "ok" || d.Runs != 1 || !d.Enabled || d.NextRun.In(tash).Format("2006-01-02 15:04") != "2026-10-17 09:30" {
		t.Fatalf("har kunlik: %+v", d)
	}
	o, _ := st.GetSchedule(ctx, 7, once)
	if o.Enabled || o.NextRun != nil || o.LastStatus != "ok" {
		t.Fatalf("bir martalik o'chishi kerak: %+v", o)
	}

	// Server 3 kun o'chiq turdi: vazifa bir marta bajariladi, keyingisi hozirdan.
	*clock = at("2026-10-20 12:00")
	r.Tick(ctx)
	r.Wait()
	d, _ = st.GetSchedule(ctx, 7, daily)
	if ai.count() != 3 || d.NextRun.In(tash).Format("2006-01-02 15:04") != "2026-10-21 09:30" {
		t.Fatalf("o'tkazib yuborilganlar yig'ilmasligi kerak: %d %v", ai.count(), d.NextRun)
	}
}

func TestRunnerPluginsAndErrors(t *testing.T) {
	r, st, ai, n, clock := newRunner(t)
	ctx := context.Background()
	sealed, _ := plugin.Seal(plainVault{}, 7, "web", plugin.Config{})
	_ = st.SavePluginConfig(ctx, store.PluginConfig{OwnerID: 7, PluginID: "web", Enabled: true, Config: sealed})
	_ = st.SavePluginConfig(ctx, store.PluginConfig{OwnerID: 7, PluginID: "weather", Enabled: false})
	id := create(t, st, store.Schedule{Prompt: "yangiliklar", Kind: store.ScheduleHourly, At: "00:05"}, *clock)
	if err := r.RunNow(ctx, 7, id); err != nil {
		t.Fatal(err)
	}
	r.Wait()
	if req := ai.reqs[0]; len(req.Tools) != 1 || req.Tools[0].Name != "web_search" {
		t.Fatalf("faqat yoqilgan plagin vositalari: %+v", req.Tools)
	}
	if m := n.all(); len(m) != 1 || !strings.Contains(m[0], "web:q") {
		t.Fatalf("plagin natijasi: %q", m)
	}
	s, _ := st.GetSchedule(ctx, 7, id)
	if s.NextRun == nil || s.NextRun.In(tash).Format("15:04") != "09:05" {
		t.Fatalf("qo'lda bajarish jadvalni o'zgartirmaydi: %v", s.NextRun)
	}
	if err := r.RunNow(ctx, 8, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("begona: %v", err)
	}

	// Pro bo'lmasa (Advanced) — vazifa plaginsiz bajariladi.
	r.Pro = func(context.Context, int64) bool { return false }
	if err := r.RunNow(ctx, 7, id); err != nil {
		t.Fatal(err)
	}
	r.Wait()
	if req := ai.reqs[len(ai.reqs)-1]; len(req.Tools) != 0 || req.Call != nil {
		t.Fatalf("Pro'siz plagin vositalari berilmasligi kerak: %+v", req.Tools)
	}
	r.Pro = nil

	// Bajarilayotgan vazifa qayta boshlanmaydi.
	_, _ = st.MarkScheduleRunning(ctx, 7, id, *clock)
	if err := r.RunNow(ctx, 7, id); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy: %v", err)
	}
	_ = st.FinishSchedule(ctx, 7, id, "ok", "")

	// AI xatosi: holat error, egasiga ogohlantirish.
	ai.err = errors.New("500")
	if err := r.RunNow(ctx, 7, id); err != nil {
		t.Fatal(err)
	}
	r.Wait()
	s, _ = st.GetSchedule(ctx, 7, id)
	if s.LastStatus != "error" || !strings.Contains(n.all()[len(n.all())-1], "bajarilmadi") {
		t.Fatalf("xato: %+v %q", s, n.all())
	}

	// Obuna tugagan: AI chaqirilmaydi.
	r.Entitled = func(context.Context, int64) bool { return false }
	before := ai.count()
	if err := r.RunNow(ctx, 7, id); !errors.Is(err, ErrNotEntitle) {
		t.Fatalf("obuna: %v", err)
	}
	*clock = at("2026-10-16 09:05")
	r.Tick(ctx)
	r.Wait()
	s, _ = st.GetSchedule(ctx, 7, id)
	if ai.count() != before || s.LastStatus != "error" || s.NextRun.In(tash).Format("15:04") != "10:05" {
		t.Fatalf("obunasiz tick: %d %+v", ai.count(), s)
	}
}

func TestRunnerDailyLimit(t *testing.T) {
	r, st, ai, _, clock := newRunner(t)
	ctx := context.Background()
	id := create(t, st, store.Schedule{Prompt: "x", Kind: store.ScheduleDaily, At: "10:00"}, *clock)
	for i := 0; i < MaxRunsPerDay; i++ {
		st.Audit(ctx, 7, "schedule_run", "")
	}
	if err := r.RunNow(ctx, 7, id); !errors.Is(err, ErrDailyLimit) || ai.count() != 0 {
		t.Fatalf("kunlik chegara: %v", err)
	}
}

func TestRunnerDeliverToContact(t *testing.T) {
	r, st, ai, n, clock := newRunner(t)
	ctx := context.Background()
	type call struct {
		chat int64
		auto bool
	}
	var mu sync.Mutex
	var calls []call
	result, deliverErr := true, error(nil)
	r.Deliver = func(_ context.Context, owner, chatID int64, name, title, text string, auto bool) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, call{chatID, auto})
		return result, deliverErr
	}
	id := create(t, st, store.Schedule{Title: "Tong", Prompt: "xayrli tong tilagi", Kind: store.ScheduleDaily, At: "09:30",
		TargetID: 55, TargetName: "Onam", TargetAuto: true}, *clock)
	if err := r.RunNow(ctx, 7, id); err != nil {
		t.Fatal(err)
	}
	r.Wait()
	if len(calls) != 1 || calls[0] != (call{55, true}) {
		t.Fatalf("deliver: %+v", calls)
	}
	if rcp := ai.reqs[0].Recipient; rcp != "Onam" {
		t.Fatalf("AI'ga qabul qiluvchi berilmadi: %q", rcp)
	}
	if msgs := n.all(); len(msgs) != 1 || !strings.Contains(msgs[0], "Onamga yuborildi") {
		t.Fatalf("egasiga xabar: %q", msgs)
	}
	// Qoralama: runner o'zi xabar yubormaydi (Deliver yuboradi).
	result = false
	_ = r.RunNow(ctx, 7, id)
	r.Wait()
	if len(n.all()) != 1 {
		t.Fatalf("qoralamada ortiqcha xabar: %q", n.all())
	}
	// Xato: natija egasiga sabab bilan.
	deliverErr = errors.New("sessiya yo'q")
	_ = r.RunNow(ctx, 7, id)
	r.Wait()
	if msgs := n.all(); len(msgs) != 2 || !strings.Contains(msgs[1], "yuborilmadi: sessiya yo'q") {
		t.Fatalf("xato xabari: %q", msgs)
	}
	// Pro yo'q: kontaktga yuborilmaydi, natija egasiga.
	r.Pro = func(context.Context, int64) bool { return false }
	_ = r.RunNow(ctx, 7, id)
	r.Wait()
	if len(calls) != 3 || !strings.Contains(n.all()[2], "Pro tarifda") {
		t.Fatalf("pro'siz: calls=%d %q", len(calls), n.all())
	}
}
