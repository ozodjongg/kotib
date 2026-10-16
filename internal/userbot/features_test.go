package userbot

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
	"github.com/ozodjongg/secretar_ai_agent/internal/miniapp"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// fakeAnalyst — Reply'dan tashqari egasi uchun topshiriqlarni ham bajaradi.
type fakeAnalyst struct {
	fakeLLM
	amu        sync.Mutex
	summaries  []llm.SummaryRequest
	styles     []llm.StyleRequest
	answers    []llm.AnswerRequest
	greetings  []llm.GreetingRequest
	transcribe int
	score      int
}

func (f *fakeAnalyst) Summarize(_ context.Context, r llm.SummaryRequest) (string, error) {
	f.amu.Lock()
	defer f.amu.Unlock()
	f.summaries = append(f.summaries, r)
	return "• " + r.Chats[0].Name + " — uchrashuv so'radi ❗", nil
}

func (f *fakeAnalyst) LearnStyle(_ context.Context, r llm.StyleRequest) (llm.StyleResult, error) {
	f.amu.Lock()
	defer f.amu.Unlock()
	f.styles = append(f.styles, r)
	score := -1
	if len(r.Samples) > 0 {
		score = f.score
	}
	return llm.StyleResult{Profile: "Qisqa yozadi, «xop» deydi.", Score: score}, nil
}

func (f *fakeAnalyst) Answer(_ context.Context, r llm.AnswerRequest) (string, error) {
	f.amu.Lock()
	defer f.amu.Unlock()
	f.answers = append(f.answers, r)
	return "Ertaga 15:00 da kelishgansiz.", nil
}

func (f *fakeAnalyst) Transcribe(context.Context, *llm.Media) (string, error) {
	f.amu.Lock()
	defer f.amu.Unlock()
	f.transcribe++
	return "ertaga kelaman", nil
}

func (f *fakeAnalyst) Greeting(_ context.Context, r llm.GreetingRequest) (string, error) {
	f.amu.Lock()
	defer f.amu.Unlock()
	f.greetings = append(f.greetings, r)
	return "Tug'ilgan kuning bilan, " + r.Name + "!", nil
}

// noteEnv — pipeline + bildirishnomalar + akkaunt (faollik yozilishi uchun).
func noteEnv(t *testing.T, mutate func(*config.Settings)) (*env, *fakeAnalyst, *[]string) {
	t.Helper()
	e := newEnv(t, mutate)
	an := &fakeAnalyst{fakeLLM: fakeLLM{reply: "Yaxshi, rahmat!"}}
	e.p.LLM = an
	now := time.Now()
	_ = e.st.UpsertAccount(context.Background(), store.Account{OwnerID: owner, Status: store.StatusActive, ConsentAt: &now})
	var mu sync.Mutex
	var notes []string
	e.p.Notify = func(_ context.Context, _ int64, text string, _ bool) {
		mu.Lock()
		notes = append(notes, text)
		mu.Unlock()
	}
	return e, an, &notes
}

func named(text string) Incoming {
	i := in(text, true)
	i.Contact.Name = "Aziz"
	return i
}

func TestUrgentAlertEvenInQuietHours(t *testing.T) {
	quiet := func(s *config.Settings) {
		s.Quiet = config.QuietHours{Enabled: true, Start: "11:00", End: "13:00", TZ: "Asia/Tashkent"}
	}
	e, an, notes := noteEnv(t, quiet)
	ctx := context.Background()
	e.p.Handle(ctx, named("SROCHNO qo'ng'iroq qil, срочно!"))
	if len(*notes) != 1 || !strings.Contains((*notes)[0], "🚨 Aziz shoshilinch") || strings.Contains((*notes)[0], "SROCHNO") {
		t.Fatalf("ogohlantirish (matnsiz): %v", *notes)
	}
	if an.calls != 0 || len(e.drafts()) != 0 {
		t.Fatal("tinch soatlarda javob tayyorlanmasligi kerak")
	}
	e.p.Handle(ctx, named("shoshilinch!"))
	if len(*notes) != 1 {
		t.Fatal("30 daqiqada bir martadan ko'p emas")
	}
	cs, _ := e.st.ListChats(ctx, owner, time.Time{})
	if len(cs) != 1 || cs[0].Private || cs[0].Name != "Aziz" || !cs[0].Unanswered() {
		t.Fatalf("faollik tinch soatda ham yozilishi kerak: %+v", cs)
	}

	// Matn faqat "Qoralamalar bot chatida" yoqilganda.
	e2, _, notes2 := noteEnv(t, func(s *config.Settings) { quiet(s); s.DraftsInBot = true })
	e2.p.Handle(ctx, named("urgent: call me"))
	if len(*notes2) != 1 || !strings.Contains((*notes2)[0], "urgent: call me") {
		t.Fatalf("%v", *notes2)
	}
	// O'chirilgan bo'lsa — yo'q.
	e3, _, notes3 := noteEnv(t, func(s *config.Settings) { s.NoUrgent = true })
	e3.p.Handle(ctx, named("shoshilinch"))
	for _, n := range *notes3 {
		if strings.Contains(n, "🚨") {
			t.Fatal("NoUrgent")
		}
	}
}

func TestPrivateChatNotProcessed(t *testing.T) {
	off := config.ModeOff
	e, _, notes := noteEnv(t, func(s *config.Settings) {
		s.Rules = []config.Rule{{ID: "r", Match: config.Match{UserIDs: []int64{100}}, Mode: &off}}
	})
	ctx := context.Background()
	e.p.Handle(ctx, named("shoshilinch"))
	if len(*notes) != 0 {
		t.Fatalf("yopiq chat uchun ogohlantirish yo'q: %v", *notes)
	}
	cs, _ := e.st.ListChats(ctx, owner, time.Time{})
	if len(cs) != 1 || !cs[0].Private {
		t.Fatalf("private belgilanishi kerak: %+v", cs)
	}
	if un, _ := e.st.ListUnanswered(ctx, time.Now().Add(time.Hour), 10); len(un) != 0 {
		t.Fatal("private chat eslatilmaydi")
	}
}

func TestKnowledgeAndContactNotePassed(t *testing.T) {
	e, an, _ := noteEnv(t, func(s *config.Settings) {
		s.Knowledge = "Ish vaqti 9–18"
		s.ContactNotes = map[int64]string{100: "doimiy mijoz"}
	})
	e.p.Handle(context.Background(), named("qachongacha ishlaysiz?"))
	if an.last.Knowledge != "Ish vaqti 9–18" || an.last.ContactNote != "doimiy mijoz" {
		t.Fatalf("%+v", an.last)
	}
}

func TestShadowModeLearnsSilently(t *testing.T) {
	until := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	e, an, notes := noteEnv(t, func(s *config.Settings) { s.Mode = config.ModeAuto; s.ShadowUntil = &until })
	ctx := context.Background()
	e.p.Handle(ctx, named("ertaga kelasanmi?"))
	if an.calls != 1 || e.tg.sentCount() != 0 || len(e.drafts()) != 0 || len(*notes) != 0 {
		t.Fatalf("soya rejimi jim bo'lishi kerak: calls=%d sent=%d drafts=%d notes=%v", an.calls, e.tg.sentCount(), len(e.drafts()), *notes)
	}
	if n, _ := e.st.CountAudit(ctx, owner, []string{"shadow_prepared"}, time.Time{}); n != 1 {
		t.Fatal("audit shadow_prepared")
	}
	e.p.OwnerMessage(ctx, 100, "xop")
	ss, _ := e.st.ListStyleSamples(ctx, owner, 0)
	if len(ss) != 1 || ss[0].Kind != store.SampleShadow || ss[0].Predicted != "Yaxshi, rahmat!" || ss[0].Actual != "xop" || ss[0].Incoming != "ertaga kelasanmi?" {
		t.Fatalf("juftlik: %+v", ss)
	}
	cs, _ := e.st.ListChats(ctx, owner, time.Time{})
	if len(cs) != 1 || cs[0].Unanswered() {
		t.Fatalf("egasi yozdi — javob berilgan: %+v", cs)
	}
	// Ikkinchi xabar taxminsiz — juftlik qo'shilmaydi.
	e.p.OwnerMessage(ctx, 100, "yana")
	if ss, _ := e.st.ListStyleSamples(ctx, owner, 0); len(ss) != 1 {
		t.Fatal("taxminsiz juftlik bo'lmaydi")
	}
}

func TestOwnSendNotCountedAsOwnerMessage(t *testing.T) {
	e, _, _ := noteEnv(t, auto)
	ctx := context.Background()
	e.p.Handle(ctx, named("qalaysan?"))
	if e.tg.sentCount() != 1 {
		t.Fatal("avto yuborilishi kerak")
	}
	cs, _ := e.st.ListChats(ctx, owner, time.Time{})
	if len(cs) != 1 || cs[0].Unanswered() {
		t.Fatalf("kotib javobi faollikka yozilishi kerak: %+v", cs)
	}
	if !e.p.isOwnSend(100, "Yaxshi,  rahmat!") || e.p.isOwnSend(100, "boshqa") {
		t.Fatal("kotib yuborgan matn tanilishi kerak")
	}
}

func TestLearnFromEditedDraft(t *testing.T) {
	e, _, _ := noteEnv(t, func(s *config.Settings) { s.Learn = true })
	ctx := context.Background()
	e.p.Handle(ctx, named("kelasanmi?"))
	d := e.drafts()
	if len(d) != 1 {
		t.Fatal("qoralama kerak")
	}
	same := d[0]
	if err := e.p.SendDraft(ctx, same); err != nil {
		t.Fatal(err)
	}
	if ss, _ := e.st.ListStyleSamples(ctx, owner, 0); len(ss) != 0 {
		t.Fatal("o'zgarmagan qoralama juftlik emas")
	}
	edited := d[0]
	edited.Reply = "xop, boraman"
	if err := e.p.SendDraft(ctx, edited); err != nil {
		t.Fatal(err)
	}
	ss, _ := e.st.ListStyleSamples(ctx, owner, 0)
	if len(ss) != 1 || ss[0].Kind != store.SampleEdit || ss[0].Predicted != "Yaxshi, rahmat!" || ss[0].Actual != "xop, boraman" {
		t.Fatalf("tahrir juftligi: %+v", ss)
	}
	// O'qitish o'chiq — saqlanmaydi.
	e2, _, _ := noteEnv(t, nil)
	e2.p.Handle(ctx, named("kelasanmi?"))
	d2 := e2.drafts()[0]
	d2.Reply = "boshqa"
	_ = e2.p.SendDraft(ctx, d2)
	if ss, _ := e2.st.ListStyleSamples(ctx, owner, 0); len(ss) != 0 {
		t.Fatal("Learn o'chiq")
	}
}

func photoIn(text string, loaded *int) Incoming {
	i := named(text)
	i.Photo = &Photo{Load: func(context.Context) ([]byte, error) { *loaded++; return []byte("JPG"), nil }}
	return i
}

func TestPhotoMessages(t *testing.T) {
	ctx := context.Background()
	var loaded int
	// O'chiq: izohsiz rasm — o'tkazib yuboriladi; izohli — faqat matn bilan.
	e, an, _ := noteEnv(t, auto)
	e.p.Handle(ctx, photoIn(PhotoText, &loaded))
	if an.calls != 0 || loaded != 0 {
		t.Fatal("rasmga javob o'chiq")
	}
	e.p.Handle(ctx, photoIn(PhotoText+"\nqalay?", &loaded))
	if an.calls != 1 || an.last.Image != nil || an.last.Incoming != "qalay?" || e.tg.sentCount() != 1 {
		t.Fatalf("faqat izoh: %+v", an.last)
	}
	// Yoqiq: rasm modelga boradi, javob faqat qoralama.
	e2, an2, _ := noteEnv(t, func(s *config.Settings) { s.Mode = config.ModeAuto; s.ReplyPhotos = true })
	e2.p.Handle(ctx, photoIn(PhotoText, &loaded))
	if an2.last.Image == nil || string(an2.last.Image.Data) != "JPG" || loaded != 1 {
		t.Fatal("rasm modelga yuborilishi kerak")
	}
	if e2.tg.sentCount() != 0 || len(e2.drafts()) != 1 || !strings.Contains(e2.drafts()[0].Reason, "rasm") {
		t.Fatalf("rasm — faqat qoralama: %+v", e2.drafts())
	}
	// Pro bo'lmasa — yo'q.
	e3, an3, _ := noteEnv(t, func(s *config.Settings) { s.ReplyPhotos = true })
	e3.p.Pro = func(context.Context, int64) bool { return false }
	e3.p.Handle(ctx, photoIn(PhotoText, &loaded))
	if an3.calls != 0 {
		t.Fatal("Pro'siz rasm yo'q")
	}
}

func TestVoiceTranscript(t *testing.T) {
	ctx := context.Background()
	var loaded int
	e, an, notes := noteEnv(t, func(s *config.Settings) { s.TranscribeVoice = true; s.ReplyVoice = true })
	msg := voiceIn(VoiceText, &loaded)
	msg.Contact.Name = "Aziz"
	msg.Voice.Duration = 65
	e.p.Handle(ctx, msg)
	if an.transcribe != 1 || len(*notes) == 0 || !strings.Contains((*notes)[0], "🎤 Aziz (1:05):\n«ertaga kelaman»") {
		t.Fatalf("transkript: %v", *notes)
	}
	if loaded != 1 || an.calls != 1 || an.last.Audio == nil {
		t.Fatalf("audio bir marta yuklanib, javobga ham ishlatilishi kerak: loaded=%d calls=%d", loaded, an.calls)
	}
	// Transkript tinch soatlarda ham (javob esa yo'q).
	e2, an2, notes2 := noteEnv(t, func(s *config.Settings) {
		s.TranscribeVoice = true
		s.Quiet = config.QuietHours{Enabled: true, Start: "11:00", End: "13:00", TZ: "Asia/Tashkent"}
	})
	e2.p.Handle(ctx, voiceIn(VoiceText, &loaded))
	if an2.transcribe != 1 || len(*notes2) != 1 || an2.calls != 0 {
		t.Fatalf("tinch soatda transkript: %v calls=%d", *notes2, an2.calls)
	}
	// Pro emas — transkript yo'q.
	e3, an3, _ := noteEnv(t, func(s *config.Settings) { s.TranscribeVoice = true })
	e3.p.Pro = func(context.Context, int64) bool { return false }
	e3.p.Handle(ctx, voiceIn(VoiceText, &loaded))
	if an3.transcribe != 0 {
		t.Fatal("Pro'siz transkript yo'q")
	}
}

// ---- Manager ----

// chatsTG — bir nechta chat tarixi, kontaktlar va oxirgi chatlar.
type chatsTG struct {
	fakeTG
	hist     map[int64][]llm.Turn
	contacts []miniapp.ContactInfo
}

func (c *chatsTG) History(_ context.Context, id int64, _ int) ([]llm.Turn, error) {
	return c.hist[id], nil
}
func (c *chatsTG) Contacts(context.Context) ([]miniapp.ContactInfo, error) { return c.contacts, nil }
func (c *chatsTG) RecentChats(context.Context, int) ([]miniapp.ContactInfo, error) {
	return c.contacts, nil
}

func analystManager(t *testing.T, tg Telegram, mutate func(*config.Settings)) (*Manager, *store.Memory, *fakeAnalyst, *[]string) {
	t.Helper()
	m, st, notes := runningManager(t, tg)
	an := &fakeAnalyst{score: 80}
	m.LLM = an
	if mutate != nil {
		s, _ := st.GetSettings(context.Background(), owner)
		mutate(&s)
		_ = st.SaveSettings(context.Background(), owner, s)
	}
	return m, st, an, notes
}

func TestSmartDigest(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Tashkent")
	evening := time.Now().In(loc)
	evening = time.Date(evening.Year(), evening.Month(), evening.Day(), 21, 10, 0, 0, loc)
	old := evening.Add(-30 * time.Hour)
	tg := &chatsTG{hist: map[int64][]llm.Turn{
		100: {{Text: "kechagi", At: old}, {Text: "ertaga uchrashamizmi?", At: evening.Add(-time.Hour)}},
		101: {{FromOwner: true, Text: "salom", At: evening.Add(-2 * time.Hour)}},
	}}
	m, st, an, notes := analystManager(t, tg, func(s *config.Settings) { s.SmartDigest = true })
	ctx := context.Background()
	_ = st.ChatIn(ctx, store.ChatActivity{OwnerID: owner, ChatID: 100, Name: "Aziz", LastIn: evening.Add(-time.Hour)})
	_ = st.ChatIn(ctx, store.ChatActivity{OwnerID: owner, ChatID: 101, Name: "Vali", LastIn: evening.Add(-3 * time.Hour)})
	_ = st.ChatOut(ctx, owner, 101, evening.Add(-2*time.Hour))
	_ = st.ChatIn(ctx, store.ChatActivity{OwnerID: owner, ChatID: 102, Name: "Sir", Private: true, LastIn: evening.Add(-time.Hour)})

	m.SendDigests(ctx, evening)
	if len(*notes) != 1 {
		t.Fatalf("xulosa: %v", *notes)
	}
	d := (*notes)[0]
	for _, want := range []string{"Javob kutayotganlar: Aziz (20:10 dan)", "Bugun nima bo'ldi", "• Aziz — uchrashuv"} {
		if !strings.Contains(d, want) {
			t.Errorf("xulosada %q yo'q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "Sir") {
		t.Error("private chat xulosaga kirmaydi")
	}
	if len(an.summaries) != 1 || len(an.summaries[0].Chats) != 1 || len(an.summaries[0].Chats[0].Turns) != 1 {
		t.Fatalf("faqat bugungi kontakt xabarli chat: %+v", an.summaries)
	}

	// Pro bo'lmasa — AI qismsiz.
	m2, st2, an2, notes2 := analystManager(t, tg, func(s *config.Settings) { s.SmartDigest = true })
	m2.Pro = func(context.Context, int64) bool { return false }
	_ = st2.ChatIn(ctx, store.ChatActivity{OwnerID: owner, ChatID: 100, Name: "Aziz", LastIn: evening.Add(-time.Hour)})
	m2.SendDigests(ctx, evening)
	if len(an2.summaries) != 0 || len(*notes2) != 1 || strings.Contains((*notes2)[0], "nima bo'ldi") {
		t.Fatalf("Pro'siz: %v", *notes2)
	}
}

func TestMorningBrief(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Tashkent")
	n := time.Now().In(loc)
	morning := time.Date(n.Year(), n.Month(), n.Day(), 8, 5, 0, 0, loc)
	tg := &chatsTG{hist: map[int64][]llm.Turn{100: {{Text: "tunda yozdim", At: morning.Add(-5 * time.Hour)}}}}
	m, st, an, notes := analystManager(t, tg, nil)
	ctx := context.Background()
	_ = st.ChatIn(ctx, store.ChatActivity{OwnerID: owner, ChatID: 100, Name: "Aziz", LastIn: morning.Add(-5 * time.Hour)})

	m.SendMorningBriefs(ctx, morning)
	if len(*notes) != 0 {
		t.Fatal("default o'chiq")
	}
	s, _ := st.GetSettings(ctx, owner)
	s.MorningBrief, s.SmartDigest = true, true
	_ = st.SaveSettings(ctx, owner, s)
	m.SendMorningBriefs(ctx, morning.Add(2*time.Hour))
	if len(*notes) != 0 {
		t.Fatal("faqat soat 8 da")
	}
	m.SendMorningBriefs(ctx, morning)
	if len(*notes) != 1 {
		t.Fatalf("brifing: %v", *notes)
	}
	for _, want := range []string{"Xayrli tong", "yozganlar: Aziz", "Javob kutayotganlar: 1", "Tunda nima bo'ldi"} {
		if !strings.Contains((*notes)[0], want) {
			t.Errorf("brifingda %q yo'q:\n%s", want, (*notes)[0])
		}
	}
	if len(an.summaries) != 1 || an.summaries[0].Period != "tunda" {
		t.Fatal("tungi xulosa")
	}
	m.SendMorningBriefs(ctx, morning.Add(10*time.Minute))
	if len(*notes) != 1 {
		t.Fatal("kuniga bir marta")
	}
}

func TestCheckUnanswered(t *testing.T) {
	m, st, _, notes := analystManager(t, &chatsTG{}, func(s *config.Settings) { s.Quiet.Enabled = false })
	ctx := context.Background()
	now := time.Now()
	_ = st.ChatIn(ctx, store.ChatActivity{OwnerID: owner, ChatID: 100, Name: "Aziz", LastIn: now.Add(-5 * time.Hour)})
	_ = st.ChatIn(ctx, store.ChatActivity{OwnerID: owner, ChatID: 101, Name: "Vali", LastIn: now.Add(-2 * time.Hour)}) // hali erta
	_ = st.ChatIn(ctx, store.ChatActivity{OwnerID: owner, ChatID: 102, Name: "Draft", LastIn: now.Add(-6 * time.Hour)})
	_, _ = st.CreateDraft(ctx, store.Draft{OwnerID: owner, ChatID: 102, Incoming: "x", Reply: "y"})

	m.CheckUnanswered(ctx, now)
	if len(*notes) != 1 || !strings.Contains((*notes)[0], "Aziz (5 soatdan beri)") || strings.Contains((*notes)[0], "Vali") || strings.Contains((*notes)[0], "Draft") {
		t.Fatalf("eslatma: %v", *notes)
	}
	m.CheckUnanswered(ctx, now.Add(10*time.Minute))
	if len(*notes) != 1 {
		t.Fatal("bir xabar uchun bir marta")
	}
	m.CheckUnanswered(ctx, now.Add(3*time.Hour))
	if len(*notes) != 2 || !strings.Contains((*notes)[1], "Vali") {
		t.Fatalf("Vali endi: %v", *notes)
	}
	// O'chirilgan — yo'q.
	s, _ := st.GetSettings(ctx, owner)
	s.NoUnanswered = true
	_ = st.SaveSettings(ctx, owner, s)
	_ = st.ChatIn(ctx, store.ChatActivity{OwnerID: owner, ChatID: 103, Name: "Gani", LastIn: now.Add(-10 * time.Hour)})
	m.CheckUnanswered(ctx, now)
	if len(*notes) != 2 {
		t.Fatal("NoUnanswered")
	}
}

func TestLearnStyleAndAsk(t *testing.T) {
	tg := &chatsTG{
		hist: map[int64][]llm.Turn{
			100: {{Text: "kelasanmi"}, {FromOwner: true, Text: "xop"}, {Text: "soat nechada"}, {FromOwner: true, Text: "15 da"}},
			101: {{Text: "salom"}}, // egasi yozmagan — o'rganishga kirmaydi
		},
		contacts: []miniapp.ContactInfo{{ID: 100, Name: "Aziz Karimov"}, {ID: 101, Name: "Vali"}},
	}
	m, st, an, notes := analystManager(t, tg, nil)
	ctx := context.Background()
	if err := m.StartLearnStyle(ctx, owner); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		s, _ := st.GetSettings(ctx, owner)
		if s.StyleProfile != "" {
			if s.StyleUpdatedAt == nil {
				t.Fatal("vaqt yozilishi kerak")
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("profil saqlanmadi")
		case <-time.After(5 * time.Millisecond):
		}
	}
	for {
		m.mu.Lock()
		busy := m.learning[owner]
		m.mu.Unlock()
		if !busy {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(an.styles) != 1 || len(an.styles[0].Chats) != 1 || an.styles[0].Chats[0].Name != "Aziz Karimov" {
		t.Fatalf("o'rganish: %+v", an.styles)
	}
	if len(*notes) != 1 || !strings.Contains((*notes)[0], "O'zimdek") {
		t.Fatalf("bildirishnoma: %v", *notes)
	}

	ans, err := m.Ask(ctx, owner, "Azizga nima deb va'da berdim?")
	if err != nil || !strings.Contains(ans, "Aziz Karimov") || !strings.Contains(ans, "15:00") {
		t.Fatalf("ask: %q %v", ans, err)
	}
	if _, err := m.Ask(ctx, owner, "Bugun ob-havo qanday?"); !errors.Is(err, ErrNoContact) {
		t.Fatalf("kontaktsiz: %v", err)
	}
	m.Pro = func(context.Context, int64) bool { return false }
	if _, err := m.Ask(ctx, owner, "Aziz?"); !errors.Is(err, ErrNeedPro) {
		t.Fatal("Pro kerak")
	}
	if err := m.StartLearnStyle(ctx, owner); !errors.Is(err, ErrNeedPro) {
		t.Fatal("Pro kerak")
	}
}

func TestMatchContact(t *testing.T) {
	cs := []miniapp.ContactInfo{{ID: 1, Name: "Aziz"}, {ID: 2, Name: "Aziz Karimov"}, {ID: 3, Name: "Onam"}, {ID: 4, Name: "Al"}}
	cases := map[string]int64{
		"Aziz bilan nima kelishdik?":     1,
		"Aziz Karimovga nima dedim":      2,
		"onamga qachon boraman dedim?":   3,
		"azizning tug'ilgan kuni qachon": 1,
	}
	for q, want := range cases {
		if c, ok := matchContact(q, cs); !ok || c.ID != want {
			t.Errorf("%q: %+v %v (kutilgan %d)", q, c, ok, want)
		}
	}
	if _, ok := matchContact("Al bilan", cs); ok {
		t.Error("qisqa ismlar mos kelmasligi kerak")
	}
	if _, ok := matchContact("Azizbekni ko'rdingmi", cs); ok {
		t.Error("boshqa ism (Azizbek) — juda uzun qo'shimcha")
	}
}

func TestRefineAndShadowReport(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Tashkent")
	n := time.Now().In(loc)
	night := time.Date(n.Year(), n.Month(), n.Day(), 3, 15, 0, 0, loc)
	m, st, an, notes := analystManager(t, &chatsTG{}, func(s *config.Settings) { s.Learn = true })
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_ = st.AddStyleSample(ctx, store.StyleSample{OwnerID: owner, Kind: store.SampleEdit, Predicted: "a", Actual: "b"})
	}
	m.RefineStyles(ctx, night)
	if len(an.styles) != 0 {
		t.Fatal("juftliklar kam — yangilanmaydi")
	}
	_ = st.AddStyleSample(ctx, store.StyleSample{OwnerID: owner, Kind: store.SampleShadow, Predicted: "a", Actual: "b"})
	m.RefineStyles(ctx, night)
	if len(an.styles) != 0 {
		t.Fatal("kuniga bir marta (birinchi urinish yozilgan)")
	}
	m.RefineStyles(ctx, night.AddDate(0, 0, 1))
	if len(an.styles) != 1 || len(an.styles[0].Samples) != 3 {
		t.Fatalf("yangilash: %+v", an.styles)
	}
	s, _ := st.GetSettings(ctx, owner)
	if s.StyleScore != 80 || s.StyleProfile == "" {
		t.Fatalf("profil/baho: %+v", s)
	}
	if ss, _ := st.ListStyleSamples(ctx, owner, 0); len(ss) != 0 {
		t.Fatal("ishlatilgan juftliklar o'chirilishi kerak")
	}

	// Soya rejimi tugadi — hisobot va tugmalar.
	var offered []string
	var btns [][2]string
	m.Offer = func(_ context.Context, _ int64, text string, b [][2]string) {
		offered = append(offered, text)
		btns = b
	}
	until := time.Now().Add(-time.Hour)
	s.ShadowUntil = &until
	_ = st.SaveSettings(ctx, owner, s)
	st.Audit(ctx, owner, "shadow_prepared", "chat=1")
	st.Audit(ctx, owner, "shadow_prepared", "chat=2")
	m.SendShadowReports(ctx, time.Now())
	if len(offered) != 1 || !strings.Contains(offered[0], "2 ta javobni") || !strings.Contains(offered[0], "80%") || len(btns) != 2 || btns[0][1] != "shd:auto:1" {
		t.Fatalf("hisobot: %v %v", offered, btns)
	}
	m.SendShadowReports(ctx, time.Now())
	if len(offered) != 1 || len(*notes) != 0 {
		t.Fatal("bir marta")
	}
	msg, err := m.ApplyShadowChoice(ctx, owner, ShadowAuto)
	s, _ = st.GetSettings(ctx, owner)
	if err != nil || msg == "" || s.Mode != config.ModeAuto || s.DefaultPersona != config.PersonaMine {
		t.Fatalf("avto tanlov: %v %+v", err, s)
	}
	if _, err := m.ApplyShadowChoice(ctx, owner, "shd:x"); err == nil {
		t.Fatal("noma'lum tanlov")
	}
}

// bdGoogle — Google + Birthdays.
type bdGoogle struct {
	people []Birthday
}

func (g *bdGoogle) Connected(context.Context, int64) bool { return true }
func (g *bdGoogle) Busy(context.Context, int64, time.Time, time.Time) ([]llm.Interval, error) {
	return nil, nil
}
func (g *bdGoogle) Contact(context.Context, int64, int64, string, string) (string, string) {
	return "", ""
}
func (g *bdGoogle) Birthdays(context.Context, int64, time.Month, int) ([]Birthday, error) {
	return g.people, nil
}

func TestSendBirthdays(t *testing.T) {
	tg := &chatsTG{contacts: []miniapp.ContactInfo{{ID: 100, Name: "Aziz", Phone: "998901234567"}}}
	m, st, an, notes := analystManager(t, tg, nil)
	m.Google = &bdGoogle{people: []Birthday{{Name: "Aziz aka", Phone: "901234567"}, {Name: "Notanish"}}}
	ctx := context.Background()
	loc, _ := time.LoadLocation("Asia/Tashkent")
	n := time.Now().In(loc)
	nine := time.Date(n.Year(), n.Month(), n.Day(), 9, 1, 0, 0, loc)
	m.SendBirthdays(ctx, nine.Add(-time.Hour))
	if len(an.greetings) != 0 {
		t.Fatal("faqat soat 9 da")
	}
	m.SendBirthdays(ctx, nine)
	if len(an.greetings) != 1 || an.greetings[0].Name != "Aziz" {
		t.Fatalf("tabrik: %+v", an.greetings)
	}
	ds, _ := st.ListDrafts(ctx, owner, store.DraftPending, 10)
	if len(ds) != 1 || ds[0].ChatID != 100 || ds[0].ReplyToMsg != 0 || !strings.Contains(ds[0].Reply, "Tug'ilgan kuning") {
		t.Fatalf("qoralama: %+v", ds)
	}
	found := false
	for _, s := range *notes {
		if strings.Contains(s, "Notanish") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Telegram'da topilmagan — eslatma: %v", *notes)
	}
	m.SendBirthdays(ctx, nine.Add(10*time.Minute))
	if len(an.greetings) != 1 {
		t.Fatal("kuniga bir marta")
	}
}
