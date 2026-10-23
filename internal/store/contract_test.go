package store_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
	"github.com/ozodjongg/secretar_ai_agent/migrations"
)

// Bir xil xulq-atvor talablari Memory va Postgres uchun. Postgres qismi faqat
// TEST_DATABASE_URL berilganda ishlaydi (CI'da postgres service bilan).
func TestStoreContract(t *testing.T) {
	t.Run("memory", func(t *testing.T) { s := store.NewMemory(); contract(t, s); adminContract(t, s); charityContract(t, s) })
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("TEST_DATABASE_URL")
		if dsn == "" {
			t.Skip("TEST_DATABASE_URL yo'q")
		}
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		ctx := context.Background()
		for i := 0; i < 2; i++ { // migratsiya idempotent bo'lishi shart
			if err := migrations.Apply(ctx, db); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(`TRUNCATE accounts, subscriptions, payments, usage_daily, google_links, actions, hooks, followups, referral_codes, referrals, linked_accounts, link_tokens, chat_activity, style_samples, schedules, plugin_configs, bans, charity_settings, charity_payouts CASCADE`); err != nil {
			t.Fatal(err)
		}
		contract(t, store.NewPostgres(db))
		adminContract(t, store.NewPostgres(db))
		charityContract(t, store.NewPostgres(db))
	})
}

func contract(t *testing.T, s store.Store) {
	ctx := context.Background()
	if _, err := s.GetAccount(ctx, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("yo'q akkaunt: %v", err)
	}
	now := time.Now()
	for _, id := range []int64{1, 2} {
		if err := s.UpsertAccount(ctx, store.Account{OwnerID: id, Status: store.StatusActive, ConsentAt: &now}); err != nil {
			t.Fatal(err)
		}
	}
	// consent_at saqlanib qolishi kerak (nil bilan upsert o'chirmasin)
	if err := s.UpsertAccount(ctx, store.Account{OwnerID: 1, Status: store.StatusActive}); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.GetAccount(ctx, 1); a.ConsentAt == nil {
		t.Fatal("consent yo'qoldi")
	}
	if ids, _ := s.ListActiveOwners(ctx); len(ids) != 2 {
		t.Fatalf("faol: %v", ids)
	}

	if err := s.SaveSession(ctx, 1, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if b, err := s.LoadSession(ctx, 1); err != nil || len(b) != 3 {
		t.Fatalf("session: %v %v", b, err)
	}

	st := config.Default()
	st.Mode = config.ModeAuto
	if err := s.SaveSettings(ctx, 1, st); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetSettings(ctx, 1); got.Mode != config.ModeAuto {
		t.Fatal("settings")
	}
	if got, _ := s.GetSettings(ctx, 2); got.Mode != config.ModeDraft {
		t.Fatal("default settings")
	}

	id, err := s.CreateDraft(ctx, store.Draft{OwnerID: 1, ChatID: 5, Incoming: "a", Reply: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDraft(ctx, 2, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("begona draft ko'rinmasligi kerak")
	}
	if err := s.ResolveDraft(ctx, 2, id, store.DraftSent); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("begona draftni hal qilib bo'lmaydi")
	}
	if err := s.ResolveDraft(ctx, 1, id, store.DraftSent); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveDraft(ctx, 1, id, store.DraftSent); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("ikki marta hal qilinmasligi kerak")
	}
	if err := s.ReopenDraft(ctx, 1, id); err != nil {
		t.Fatal(err)
	}
	if ds, _ := s.ListDrafts(ctx, 1, store.DraftPending, 10); len(ds) != 1 {
		t.Fatalf("reopen'dan keyin pending: %d", len(ds))
	}
	if _, err := s.ExpireDrafts(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.GetDraft(ctx, 1, id); d.Status != store.DraftExpired || d.Incoming != "" {
		t.Fatalf("expire: %+v", d)
	}

	s.Audit(ctx, 1, "x", "")
	if _, err := s.PruneAudit(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if es, err := s.ListAudit(ctx, 1, 10); err != nil || len(es) != 0 {
		t.Fatalf("prune'dan keyin audit bo'sh: %v %v", es, err)
	}
	s.Audit(ctx, 1, "a", "")
	s.Audit(ctx, 2, "b", "")
	if es, _ := s.ListAudit(ctx, 1, 10); len(es) != 1 || es[0].Event != "a" {
		t.Fatalf("audit izolyatsiyasi: %v", es)
	}

	// ---- billing ----
	if _, err := s.GetSubscription(ctx, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("obuna yo'q bo'lishi kerak")
	}
	trial := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	if err := s.SaveSubscription(ctx, store.Subscription{OwnerID: 1, TrialUntil: trial}); err != nil {
		t.Fatal(err)
	}
	sub, err := s.GetSubscription(ctx, 1)
	if err != nil || !sub.TrialUntil.Equal(trial) || !sub.PaidUntil.IsZero() {
		t.Fatalf("subscription: %+v %v", sub, err)
	}
	if ls, _ := s.ListSubscriptionsEnding(ctx, time.Now(), time.Now().Add(72*time.Hour)); len(ls) != 1 {
		t.Fatalf("ending: %v", ls)
	}
	if ls, _ := s.ListSubscriptionsEnding(ctx, time.Now(), time.Now().Add(time.Hour)); len(ls) != 0 {
		t.Fatalf("ending (tor oraliq): %v", ls)
	}
	if ok, err := s.AddPayment(ctx, store.Payment{ChargeID: "c1", OwnerID: 1, Amount: 100, Currency: "XTR"}); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := s.AddPayment(ctx, store.Payment{ChargeID: "c1", OwnerID: 1, Amount: 100, Currency: "XTR"}); ok {
		t.Fatal("takroriy charge_id qabul qilinmasligi kerak")
	}
	if p, err := s.GetPayment(ctx, "c1"); err != nil || p.Amount != 100 {
		t.Fatal(p, err)
	}
	if err := s.MarkRefunded(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRefunded(ctx, "c1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("ikki marta refund bo'lmasligi kerak")
	}
	if stt, err := s.Stats(ctx, time.Now()); err != nil || stt.Accounts != 2 || stt.StarsTotal != 0 {
		t.Fatalf("stats: %+v %v", stt, err)
	}

	for i := 0; i < 2; i++ {
		if ok, err := s.UseDaily(ctx, 1, "2026-10-13", 2); !ok || err != nil {
			t.Fatalf("limit ichida %d: %v %v", i, ok, err)
		}
	}
	if ok, err := s.UseDaily(ctx, 1, "2026-10-13", 2); ok || err != nil {
		t.Fatalf("limitdan oshmasligi kerak: %v %v", ok, err)
	}
	if ok, _ := s.UseDaily(ctx, 1, "2026-10-14", 2); !ok {
		t.Fatal("yangi kun — yangi hisob")
	}
	if ok, _ := s.UseDaily(ctx, 2, "2026-10-13", 2); !ok {
		t.Fatal("har egasi alohida")
	}

	if _, err := s.GetGoogleLink(ctx, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.SaveGoogleLink(ctx, store.GoogleLink{OwnerID: 1, Email: "a@x.uz", Token: []byte{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveGoogleLink(ctx, store.GoogleLink{OwnerID: 1, Email: "b@x.uz", Token: []byte{3}}); err != nil {
		t.Fatal(err)
	}
	if l, err := s.GetGoogleLink(ctx, 1); err != nil || l.Email != "b@x.uz" || len(l.Token) != 1 {
		t.Fatalf("google link: %+v %v", l, err)
	}
	aid, err := s.CreateAction(ctx, store.Action{OwnerID: 1, Kind: store.ActionEvent, Payload: []byte(`{"title":"x"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAction(ctx, 2, aid); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("boshqa egasining amali ko'rinmasligi kerak")
	}
	if a, err := s.GetAction(ctx, 1, aid); err != nil || a.Status != store.ActionPending || a.Kind != store.ActionEvent || string(a.Payload) != `{"title": "x"}` && string(a.Payload) != `{"title":"x"}` {
		t.Fatalf("action: %+v %v", a, err)
	}
	if err := s.SetActionStatus(ctx, 1, aid, store.ActionPending, store.ActionDone); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActionStatus(ctx, 1, aid, store.ActionPending, store.ActionDone); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("ikki marta bajarilmasligi kerak")
	}
	if n, err := s.PruneActions(ctx, time.Now().Add(-time.Hour)); err != nil || n != 0 {
		t.Fatal(n, err)
	}

	if err := s.SetHook(ctx, 1, []byte("h1")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetHook(ctx, 1, []byte("h2")); err != nil { // yangisi eskisini almashtiradi
		t.Fatal(err)
	}
	if _, err := s.OwnerByHook(ctx, []byte("h1")); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("eski havola ishlamasligi kerak")
	}
	if o, err := s.OwnerByHook(ctx, []byte("h2")); err != nil || o != 1 {
		t.Fatal(o, err)
	}
	if ok, _ := s.HasHook(ctx, 1); !ok {
		t.Fatal("has hook")
	}

	if n, err := s.CountAudit(ctx, 1, []string{"x_ev"}, time.Now().Add(-time.Hour)); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	s.Audit(ctx, 1, "x_ev", "")
	s.Audit(ctx, 1, "x_ev", "")
	s.Audit(ctx, 1, "y_ev", "")
	s.Audit(ctx, 2, "x_ev", "")
	if n, err := s.CountAudit(ctx, 1, []string{"x_ev", "y_ev"}, time.Now().Add(-time.Hour)); err != nil || n != 3 {
		t.Fatalf("count: %d %v", n, err)
	}
	if n, _ := s.CountAudit(ctx, 1, []string{"x_ev"}, time.Now().Add(time.Hour)); n != 0 {
		t.Fatal("since dan oldingilar sanalmasligi kerak")
	}
	due := time.Now().Add(-time.Minute)
	if err := s.SaveFollowup(ctx, store.Followup{OwnerID: 1, ChatID: 100, Hash: "a", DueAt: due}); err != nil {
		t.Fatal(err)
	}
	_ = s.SaveFollowup(ctx, store.Followup{OwnerID: 1, ChatID: 101, Hash: "b", DueAt: time.Now().Add(time.Hour)})
	_ = s.SaveFollowup(ctx, store.Followup{OwnerID: 1, ChatID: 100, Hash: "c", DueAt: due}) // almashtiradi
	fs, err := s.ListDueFollowups(ctx, time.Now(), 10)
	if err != nil || len(fs) != 1 || fs[0].ChatID != 100 || fs[0].Hash != "c" || fs[0].Status != store.FollowupPending {
		t.Fatalf("due: %+v %v", fs, err)
	}
	if err := s.SetFollowupStatus(ctx, 1, 100, store.FollowupReminded); err != nil {
		t.Fatal(err)
	}
	if fs, _ := s.ListDueFollowups(ctx, time.Now(), 10); len(fs) != 0 {
		t.Fatal("eslatilgan qayta chiqmasligi kerak")
	}
	if fs, _ := s.ListOpenFollowups(ctx, 1); len(fs) != 2 {
		t.Fatalf("ochiq va'dalar: %+v", fs)
	}
	_ = s.SetFollowupStatus(ctx, 1, 101, store.FollowupDone)
	if fs, _ := s.ListOpenFollowups(ctx, 1); len(fs) != 1 {
		t.Fatalf("done yopilishi kerak: %+v", fs)
	}

	if err := s.DeleteAccount(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if fs, _ := s.ListOpenFollowups(ctx, 1); len(fs) != 0 {
		t.Fatal("va'dalar akkaunt bilan o'chirilishi kerak")
	}
	if _, err := s.OwnerByHook(ctx, []byte("h2")); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("havola akkaunt bilan o'chirilishi kerak")
	}
	if _, err := s.GetGoogleLink(ctx, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("google ulanishi akkaunt bilan o'chirilishi kerak")
	}
	if _, err := s.GetAction(ctx, 1, aid); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("amallar akkaunt bilan o'chirilishi kerak")
	}
	if ok, _ := s.UseDaily(ctx, 1, "2026-10-13", 2); ok {
		t.Fatal("akkaunt o'chirilsa ham kunlik hisob saqlanishi kerak")
	}
	if _, err := s.GetSubscription(ctx, 1); err != nil {
		t.Fatal("obuna yozuvi akkaunt o'chirilganda saqlanishi kerak (qayta sinov bo'lmasin)")
	}
	if _, err := s.LoadSession(ctx, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("session o'chirilishi kerak")
	}
	if _, err := s.GetDraft(ctx, 1, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("draft o'chirilishi kerak")
	}
	referralContract(t, s)
	linkContract(t, s)
	scheduleContract(t, s)
	activityContract(t, s)
}

func activityContract(t *testing.T, s store.Store) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	if err := s.UpsertAccount(ctx, store.Account{OwnerID: 30, Status: store.StatusActive, ConsentAt: &now}); err != nil {
		t.Fatal(err)
	}
	if err := s.ChatIn(ctx, store.ChatActivity{OwnerID: 999, ChatID: 1, LastIn: now}); err == nil {
		t.Fatal("akkauntsiz faollik yozilmasligi kerak")
	}
	h := func(n int) time.Time { return now.Add(time.Duration(n) * time.Hour) }
	_ = s.ChatIn(ctx, store.ChatActivity{OwnerID: 30, ChatID: 100, Name: "Aziz", LastIn: h(-5)})
	_ = s.ChatIn(ctx, store.ChatActivity{OwnerID: 30, ChatID: 101, Name: "Vali", LastIn: h(-6)})
	_ = s.ChatOut(ctx, 30, 101, h(-5)) // Valiga javob berilgan
	_ = s.ChatIn(ctx, store.ChatActivity{OwnerID: 30, ChatID: 102, Name: "Sir", Private: true, LastIn: h(-7)})
	_ = s.ChatIn(ctx, store.ChatActivity{OwnerID: 30, ChatID: 103, LastIn: h(-1)}) // yangi — hali erta
	_ = s.ChatOut(ctx, 30, 104, h(-48))                                            // faqat chiquvchi, eski

	un, err := s.ListUnanswered(ctx, h(-4), 10)
	if err != nil || len(un) != 1 || un[0].ChatID != 100 || un[0].Name != "Aziz" || !un[0].LastIn.Equal(h(-5)) {
		t.Fatalf("javobsizlar: %+v %v", un, err)
	}
	if err := s.MarkChatReminded(ctx, 30, 100, h(-4)); err != nil {
		t.Fatal(err)
	}
	if un, _ := s.ListUnanswered(ctx, h(0), 10); len(un) != 1 || un[0].ChatID != 103 {
		t.Fatalf("eslatilgan chat qaytmasligi kerak: %+v", un)
	}
	// Kontakt yana yozdi — yangi xabar uchun yana eslatiladi; ism bo'sh bo'lsa eskisi saqlanadi.
	_ = s.ChatIn(ctx, store.ChatActivity{OwnerID: 30, ChatID: 100, LastIn: h(-3)})
	if un, _ := s.ListUnanswered(ctx, h(0), 10); len(un) != 2 || un[0].ChatID != 100 || un[0].Name != "Aziz" {
		t.Fatalf("yangi xabar: %+v", un)
	}
	if err := s.MarkChatReminded(ctx, 30, 555, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("yo'q chat: %v", err)
	}

	cs, err := s.ListChats(ctx, 30, h(-24))
	if err != nil || len(cs) != 4 || cs[0].ChatID != 103 {
		t.Fatalf("chatlar: %+v %v", cs, err)
	}
	for _, c := range cs {
		if c.ChatID == 102 && !c.Private {
			t.Fatal("private saqlanishi kerak")
		}
		if c.ChatID == 101 && (!c.LastOut.Equal(h(-5)) || c.Unanswered()) {
			t.Fatalf("chiquvchi: %+v", c)
		}
	}
	if n, err := s.PruneChats(ctx, h(-24)); err != nil || n != 1 {
		t.Fatalf("prune: %d %v", n, err)
	}

	// O'qitish juftliklari
	for i := 0; i < 3; i++ {
		if err := s.AddStyleSample(ctx, store.StyleSample{OwnerID: 30, Kind: store.SampleEdit, Incoming: "salom", Predicted: "Assalomu alaykum", Actual: "salom"}); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.AddStyleSample(ctx, store.StyleSample{OwnerID: 30, Kind: store.SampleShadow, Predicted: "ok", Actual: "xop"})
	ss, err := s.ListStyleSamples(ctx, 30, 2)
	if err != nil || len(ss) != 2 || ss[0].Kind != store.SampleEdit || ss[0].Actual != "salom" || ss[0].ID >= ss[1].ID {
		t.Fatalf("juftliklar: %+v %v", ss, err)
	}
	if err := s.DeleteStyleSamples(ctx, 30, ss[1].ID); err != nil {
		t.Fatal(err)
	}
	if ss, _ := s.ListStyleSamples(ctx, 30, 0); len(ss) != 2 || ss[1].Kind != store.SampleShadow {
		t.Fatalf("o'chirilgandan keyin: %+v", ss)
	}
	if n, err := s.PruneStyleSamples(ctx, time.Now().Add(time.Minute)); err != nil || n != 2 {
		t.Fatalf("prune juftliklar: %d %v", n, err)
	}
	for i := 0; i < store.MaxStyleSamples+3; i++ {
		_ = s.AddStyleSample(ctx, store.StyleSample{OwnerID: 30, Kind: store.SampleEdit, Predicted: "a", Actual: "b"})
	}
	if ss, _ := s.ListStyleSamples(ctx, 30, 0); len(ss) != store.MaxStyleSamples {
		t.Fatalf("chegara: %d", len(ss))
	}

	if err := s.DeleteAccount(ctx, 30); err != nil {
		t.Fatal(err)
	}
	if cs, _ := s.ListChats(ctx, 30, time.Time{}); len(cs) != 0 {
		t.Fatal("faollik akkaunt bilan o'chirilishi kerak")
	}
	if ss, _ := s.ListStyleSamples(ctx, 30, 0); len(ss) != 0 {
		t.Fatal("juftliklar akkaunt bilan o'chirilishi kerak")
	}
}

func referralContract(t *testing.T, s store.Store) {
	ctx := context.Background()
	// bonus_until saqlanadi va Until/ListSubscriptionsEnding'da hisobga olinadi
	bonus := time.Now().Add(30 * time.Minute).Truncate(time.Second)
	if err := s.SaveSubscription(ctx, store.Subscription{OwnerID: 100, BonusUntil: bonus, Tier: "pro"}); err != nil {
		t.Fatal(err)
	}
	if sub, err := s.GetSubscription(ctx, 100); err != nil || !sub.BonusUntil.Equal(bonus) || !sub.Until().Equal(bonus) || sub.Tier != "pro" {
		t.Fatalf("bonus: %+v %v", sub, err)
	}
	found := false
	ls, _ := s.ListSubscriptionsEnding(ctx, time.Now(), time.Now().Add(time.Hour))
	for _, x := range ls {
		found = found || x.OwnerID == 100
	}
	if !found {
		t.Fatalf("bonus tugashi ham ending'da bo'lishi kerak: %v", ls)
	}

	// kod: bir marta yaratiladi, boshqa egada band kod qabul qilinmaydi
	c, err := s.EnsureReferralCode(ctx, 100, "aaaabbbb")
	if err != nil || c != "aaaabbbb" {
		t.Fatal(c, err)
	}
	if c, _ := s.EnsureReferralCode(ctx, 100, "ccccdddd"); c != "aaaabbbb" {
		t.Fatalf("kod o'zgarmasligi kerak: %s", c)
	}
	if _, err := s.EnsureReferralCode(ctx, 101, "aaaabbbb"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("band kod: %v", err)
	}
	if o, err := s.ReferrerByCode(ctx, "aaaabbbb"); err != nil || o != 100 {
		t.Fatal(o, err)
	}
	if _, err := s.ReferrerByCode(ctx, "zzzzzzzz"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}

	// taklif: har odam bir marta, o'zini emas
	if ok, err := s.AddReferral(ctx, 101, 100); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := s.AddReferral(ctx, 101, 102); ok {
		t.Fatal("ikkinchi marta taklif qilinmasligi kerak")
	}
	if ok, _ := s.AddReferral(ctx, 100, 100); ok {
		t.Fatal("o'zini taklif qilib bo'lmaydi")
	}
	if _, err := s.AddReferral(ctx, 102, 100); err != nil {
		t.Fatal(err)
	}
	if r, err := s.GetReferral(ctx, 101); err != nil || r.ReferrerID != 100 || !r.ActivatedAt.IsZero() {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := s.GetReferral(ctx, 555); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	now := time.Now()
	if ok, _ := s.MarkReferral(ctx, 101, store.ReferralActivated, now); !ok {
		t.Fatal("birinchi belgilash")
	}
	if ok, _ := s.MarkReferral(ctx, 101, store.ReferralActivated, now); ok {
		t.Fatal("bosqich bir marta belgilanadi")
	}
	if ok, _ := s.MarkReferral(ctx, 555, store.ReferralPaid, now); ok {
		t.Fatal("taklif yo'q — belgilanmaydi")
	}
	if ok, _ := s.MarkReferral(ctx, 101, store.ReferralPaid, now); !ok {
		t.Fatal("to'lov bosqichi")
	}
	if st, err := s.ReferralStats(ctx, 100); err != nil || st != (store.ReferralStats{Invited: 2, Activated: 1, Paid: 1}) {
		t.Fatalf("stats: %+v %v", st, err)
	}
	if st, _ := s.Stats(ctx, now); st.Referrals != 2 || st.RefPaid != 1 {
		t.Fatalf("admin stats: %+v", st)
	}
}

func linkContract(t *testing.T, s store.Store) {
	ctx := context.Background()
	h := []byte("0123456789abcdef0123456789abcdef")
	exp := time.Now().Add(30 * time.Minute).Truncate(time.Second)
	if err := s.SaveLinkToken(ctx, h, store.LinkToken{ParentID: 200, Label: "Ish", ExpiresAt: exp}); err != nil {
		t.Fatal(err)
	}
	if lt, err := s.GetLinkToken(ctx, h); err != nil || lt.ParentID != 200 || lt.Label != "Ish" || !lt.ExpiresAt.Equal(exp) {
		t.Fatalf("token: %+v %v", lt, err)
	}
	if err := s.DeleteLinkToken(ctx, h); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetLinkToken(ctx, h); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("token o'chirilishi kerak")
	}

	if ok, err := s.AddLink(ctx, store.Link{ChildID: 201, ParentID: 200, Label: "Ish"}); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := s.AddLink(ctx, store.Link{ChildID: 201, ParentID: 300}); ok {
		t.Fatal("akkaunt ikki asosiyga bog'lanmasligi kerak")
	}
	if ok, _ := s.AddLink(ctx, store.Link{ChildID: 200, ParentID: 200}); ok {
		t.Fatal("o'ziga bog'lanmaydi")
	}
	_, _ = s.AddLink(ctx, store.Link{ChildID: 202, ParentID: 200, Label: "Do'kon"})
	if l, err := s.GetLink(ctx, 201); err != nil || l.ParentID != 200 || l.Label != "Ish" {
		t.Fatalf("link: %+v %v", l, err)
	}
	if _, err := s.GetLink(ctx, 200); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("asosiy akkaunt bog'lanmagan")
	}
	if ls, _ := s.ListLinks(ctx, 200); len(ls) != 2 {
		t.Fatalf("links: %+v", ls)
	}
	_ = s.DeleteLink(ctx, 201)
	if ls, _ := s.ListLinks(ctx, 200); len(ls) != 1 || ls[0].ChildID != 202 {
		t.Fatalf("o'chirish: %+v", ls)
	}

	// ListAuditSince: tartib, filtr, limit
	now := time.Now()
	if err := s.UpsertAccount(ctx, store.Account{OwnerID: 210, Status: store.StatusActive, ConsentAt: &now}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{"auto_sent", "skip", "draft_created", "auto_sent"} {
		s.Audit(ctx, 210, e, "chat=5")
	}
	es, err := s.ListAuditSince(ctx, 210, []string{"auto_sent", "draft_created"}, now.Add(-time.Minute), 0)
	if err != nil || len(es) != 3 || es[0].Event != "auto_sent" || es[1].Event != "draft_created" || es[0].Detail != "chat=5" {
		t.Fatalf("audit since: %+v %v", es, err)
	}
	if es, _ := s.ListAuditSince(ctx, 210, []string{"auto_sent"}, now.Add(-time.Minute), 1); len(es) != 1 {
		t.Fatalf("limit: %+v", es)
	}
	if es, _ := s.ListAuditSince(ctx, 210, []string{"auto_sent"}, now.Add(time.Hour), 0); len(es) != 0 {
		t.Fatalf("since: %+v", es)
	}
}

func scheduleContract(t *testing.T, s store.Store) {
	ctx := context.Background()
	now := time.Now()
	for _, id := range []int64{300, 301} {
		if err := s.UpsertAccount(ctx, store.Account{OwnerID: id, Status: store.StatusActive, ConsentAt: &now}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateSchedule(ctx, store.Schedule{OwnerID: 999, Title: "x", Prompt: "x", Kind: store.ScheduleDaily, At: "09:00"}); err == nil {
		t.Fatal("akkauntsiz vazifa yaratilmasligi kerak")
	}
	due := now.Add(-time.Minute)
	id, err := s.CreateSchedule(ctx, store.Schedule{OwnerID: 300, Title: "Ob-havo", Prompt: "bugungi ob-havo", Kind: store.ScheduleWeekly,
		At: "07:30", Weekdays: 1<<1 | 1<<3, Enabled: true, NextRun: &due})
	if err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Hour)
	id2, _ := s.CreateSchedule(ctx, store.Schedule{OwnerID: 300, Title: "Kurs", Prompt: "dollar", Kind: store.ScheduleOnce,
		At: "10:00", Date: "2026-10-20", Enabled: true, NextRun: &later, TargetID: 55, TargetName: "Onam", TargetAuto: true})
	if g := got2(t, s, id2); g.TargetID != 55 || g.TargetName != "Onam" || !g.TargetAuto {
		t.Fatalf("qabul qiluvchi: %+v", g)
	}
	g2 := got2(t, s, id2)
	g2.TargetID, g2.TargetName, g2.TargetAuto = 0, "", false
	if err := s.UpdateSchedule(ctx, g2); err != nil {
		t.Fatal(err)
	}
	if g := got2(t, s, id2); g.TargetID != 0 || g.TargetName != "" || g.TargetAuto {
		t.Fatalf("qabul qiluvchi o'chirilmadi: %+v", g)
	}
	_, _ = s.CreateSchedule(ctx, store.Schedule{OwnerID: 301, Title: "O'chiq", Prompt: "x", Kind: store.ScheduleDaily, At: "08:00", Enabled: false, NextRun: &due})
	got, err := s.GetSchedule(ctx, 300, id)
	if err != nil || got.Title != "Ob-havo" || got.Kind != store.ScheduleWeekly || got.Weekdays != 10 || !got.Enabled ||
		got.NextRun == nil || got.LastRun != nil || got.LastStatus != "" || got.CreatedAt.IsZero() {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := s.GetSchedule(ctx, 301, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("boshqa egasining vazifasi ko'rinmasligi kerak")
	}
	if ls, _ := s.ListSchedules(ctx, 300); len(ls) != 2 || ls[0].ID != id || ls[1].ID != id2 {
		t.Fatalf("list: %+v", ls)
	}
	dues, err := s.ListDueSchedules(ctx, now, 10)
	if err != nil || len(dues) != 1 || dues[0].ID != id {
		t.Fatalf("due (o'chiq va muddati kelmagan kirmaydi): %+v %v", dues, err)
	}
	next := now.Add(48 * time.Hour)
	if ok, err := s.ClaimSchedule(ctx, 300, id, *dues[0].NextRun, &next, now); !ok || err != nil {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if ok, _ := s.ClaimSchedule(ctx, 300, id, *dues[0].NextRun, &next, now); ok {
		t.Fatal("ikkinchi marta egallanmasligi kerak")
	}
	got, _ = s.GetSchedule(ctx, 300, id)
	if got.LastStatus != "running" || got.LastRun == nil || got.NextRun == nil || got.NextRun.Sub(next).Abs() > time.Millisecond || !got.Enabled {
		t.Fatalf("claim'dan keyin: %+v", got)
	}
	if ok, _ := s.MarkScheduleRunning(ctx, 300, id, now.Add(time.Minute)); ok {
		t.Fatal("bajarilayotgan vazifa qayta boshlanmasligi kerak")
	}
	if ok, _ := s.MarkScheduleRunning(ctx, 300, id, now.Add(time.Hour)); !ok {
		t.Fatal("qotib qolgan running holatidan keyin qayta boshlash mumkin")
	}
	if err := s.FinishSchedule(ctx, 300, id, "ok", "Toshkent +20"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.MarkScheduleRunning(ctx, 300, id, now.Add(time.Hour)); !ok {
		t.Fatal("tugagan vazifani qo'lda boshlash mumkin")
	}
	_ = s.FinishSchedule(ctx, 300, id, "error", "xato")
	got, _ = s.GetSchedule(ctx, 300, id)
	if got.LastStatus != "error" || got.LastResult != "xato" || got.Runs != 2 {
		t.Fatalf("finish: %+v", got)
	}
	if _, err := s.MarkScheduleRunning(ctx, 301, id, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("begona: %v", err)
	}
	// Bir martalik: egallanganda o'chadi.
	if ok, _ := s.ClaimSchedule(ctx, 300, id2, *got2(t, s, id2).NextRun, nil, now); !ok {
		t.Fatal("once claim")
	}
	if g := got2(t, s, id2); g.Enabled || g.NextRun != nil {
		t.Fatalf("bir martalik o'chishi kerak: %+v", g)
	}
	// Tahrirlash: statistika saqlanadi.
	got.Title, got.Prompt, got.Kind, got.At, got.Weekdays, got.Enabled, got.NextRun = "Yangi", "p2", store.ScheduleDaily, "08:15", 0, false, nil
	if err := s.UpdateSchedule(ctx, got); err != nil {
		t.Fatal(err)
	}
	if g := got2(t, s, id); g.Title != "Yangi" || g.Kind != store.ScheduleDaily || g.Enabled || g.NextRun != nil || g.Runs != 2 || g.LastResult != "xato" {
		t.Fatalf("update: %+v", g)
	}
	got.OwnerID = 301
	if err := s.UpdateSchedule(ctx, got); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("begona yangilay olmaydi")
	}
	if err := s.DeleteSchedule(ctx, 301, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("begona o'chira olmaydi")
	}
	if err := s.DeleteSchedule(ctx, 300, id); err != nil {
		t.Fatal(err)
	}

	// Plaginlar
	if err := s.SavePluginConfig(ctx, store.PluginConfig{OwnerID: 300, PluginID: "youtube", Enabled: true, Config: []byte{1, 2}}); err != nil {
		t.Fatal(err)
	}
	_ = s.SavePluginConfig(ctx, store.PluginConfig{OwnerID: 300, PluginID: "weather", Enabled: true})
	_ = s.SavePluginConfig(ctx, store.PluginConfig{OwnerID: 300, PluginID: "youtube", Enabled: false, Config: []byte{3}})
	ps, err := s.ListPluginConfigs(ctx, 300)
	if err != nil || len(ps) != 2 || ps[0].PluginID != "weather" || ps[1].Enabled || len(ps[1].Config) != 1 || ps[1].Config[0] != 3 {
		t.Fatalf("plugins: %+v %v", ps, err)
	}
	_ = s.DeletePluginConfig(ctx, 300, "weather")
	if ps, _ := s.ListPluginConfigs(ctx, 300); len(ps) != 1 {
		t.Fatalf("delete plugin: %+v", ps)
	}
	if ps, _ := s.ListPluginConfigs(ctx, 301); len(ps) != 0 {
		t.Fatal("boshqa ega")
	}
	// Akkaunt o'chirilsa — vazifa va plaginlar ham.
	if err := s.DeleteAccount(ctx, 300); err != nil {
		t.Fatal(err)
	}
	if ls, _ := s.ListSchedules(ctx, 300); len(ls) != 0 {
		t.Fatalf("vazifalar qoldi: %+v", ls)
	}
	if ps, _ := s.ListPluginConfigs(ctx, 300); len(ps) != 0 {
		t.Fatal("plaginlar qoldi")
	}
}

func got2(t *testing.T, s store.Store, id int64) store.Schedule {
	t.Helper()
	g, err := s.GetSchedule(context.Background(), 300, id)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func adminContract(t *testing.T, s store.Store) {
	ctx := context.Background()
	const a, b = 9001, 9002
	for _, id := range []int64{a, b} {
		if err := s.UpsertAccount(ctx, store.Account{OwnerID: id, Status: store.StatusActive}); err != nil {
			t.Fatal(err)
		}
	}
	// TouchAccount yo'q akkauntni yaratmaydi; UpsertAccount ism/username'ni o'chirmaydi.
	if err := s.TouchAccount(ctx, 9999, "X", "x", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAccount(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("touch akkaunt yaratdi: %v", err)
	}
	seen := time.Now().Truncate(time.Second)
	if err := s.TouchAccount(ctx, a, "Ali", "ali", seen); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAccount(ctx, store.Account{OwnerID: a, Status: store.StatusPaused}); err != nil {
		t.Fatal(err)
	}
	acc, err := s.GetAccount(ctx, a)
	if err != nil || acc.Name != "Ali" || acc.Username != "ali" || acc.SeenAt == nil || !acc.SeenAt.Equal(seen) {
		t.Fatalf("touch: %+v %v", acc, err)
	}
	st := config.Default()
	st.Mode = config.ModeAuto
	if err := s.SaveSettings(ctx, a, st); err != nil {
		t.Fatal(err)
	}
	s.Audit(ctx, a, "auto_sent", "chat=1")
	s.Audit(ctx, a, "auto_sent", "chat=2")
	s.Audit(ctx, a, "settings_saved", "")
	if _, err := s.GetBan(ctx, b); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ban yo'q: %v", err)
	}
	if err := s.SetBan(ctx, store.Ban{OwnerID: b, Reason: "spam", By: 1}); err != nil {
		t.Fatal(err)
	}
	if bn, err := s.GetBan(ctx, b); err != nil || bn.Reason != "spam" || bn.By != 1 {
		t.Fatalf("ban: %+v %v", bn, err)
	}
	rows, err := s.ListAccounts(ctx, time.Now().Add(-time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]store.AccountRow{}
	for _, r := range rows {
		got[r.OwnerID] = r
	}
	ra, rb := got[a], got[b]
	if ra.Settings.Mode != config.ModeAuto || ra.Counts["auto_sent"] != 2 || ra.Counts["settings_saved"] != 0 || ra.LastEventAt == nil || ra.Ban != nil || ra.Name != "Ali" {
		t.Fatalf("a qatori: %+v", ra)
	}
	if rb.Ban == nil || rb.Ban.Reason != "spam" || rb.Settings.Mode != config.Default().Mode {
		t.Fatalf("b qatori: %+v", rb)
	}
	// Ban akkaunt o'chirilgandan keyin ham qoladi.
	if err := s.DeleteAccount(ctx, b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetBan(ctx, b); err != nil {
		t.Fatalf("ban o'chib ketdi: %v", err)
	}
	if err := s.DeleteBan(ctx, b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetBan(ctx, b); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unban: %v", err)
	}
	if _, err := s.AddPayment(ctx, store.Payment{ChargeID: "adm-1", OwnerID: a, Amount: 5, Currency: "XTR"}); err != nil {
		t.Fatal(err)
	}
	ps, err := s.ListPayments(ctx, 10)
	if err != nil || len(ps) == 0 {
		t.Fatalf("payments: %v %v", ps, err)
	}
}

func charityContract(t *testing.T, s store.Store) {
	ctx := context.Background()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if got, err := s.CharityStart(ctx, start); err != nil || !got.Equal(start) {
		t.Fatalf("start: %v %v", got, err)
	}
	if got, _ := s.CharityStart(ctx, start.AddDate(0, 2, 0)); !got.Equal(start) {
		t.Fatalf("start o'zgarmasligi kerak: %v", got)
	}

	from, to := time.Now().Add(-time.Minute), time.Now().Add(time.Minute)
	subs0, dons0, err := s.PaymentSums(ctx, from, to) // avvalgi testlar to'lovlari
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []store.Payment{
		{ChargeID: "ch-s1", OwnerID: 5, Amount: 150, Currency: "XTR", Payload: "sub:5:advanced:0123456789abcdef"},
		{ChargeID: "ch-s2", OwnerID: 5, Amount: 70, Currency: "XTR", Payload: "sub:5:advanced:0123456789abcdef"},
		{ChargeID: "ch-d1", OwnerID: 6, Amount: 40, Currency: "XTR", Payload: store.DonationPrefix + "6:0123456789abcdef"},
	} {
		if _, err := s.AddPayment(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkRefunded(ctx, "ch-s2"); err != nil {
		t.Fatal(err)
	}
	subs, dons, err := s.PaymentSums(ctx, from, to)
	if err != nil || subs-subs0 != 150 || dons-dons0 != 40 {
		t.Fatalf("sums: %d %d %v", subs, dons, err)
	}
	if subs, dons, _ := s.PaymentSums(ctx, time.Now().Add(time.Minute), time.Now().Add(time.Hour)); subs != 0 || dons != 0 {
		t.Fatalf("bo'sh oraliq: %d %d", subs, dons)
	}
	if m, err := s.SubscriberSums(ctx, from, to); err != nil || m[5] != 150 || m[6] != 0 {
		t.Fatalf("subscriber sums (xayriya va refund kirmaydi): %v %v", m, err)
	}
	if subs, dons, err := s.OwnerPaymentSums(ctx, 6, from, to); err != nil || subs != 0 || dons != 40 {
		t.Fatalf("owner 6: %d %d %v", subs, dons, err)
	}
	if subs, dons, _ := s.OwnerPaymentSums(ctx, 5, from, to); subs != 150 || dons != 0 {
		t.Fatalf("owner 5: %d %d", subs, dons)
	}
	if p, _ := s.GetPayment(ctx, "ch-d1"); !p.IsDonation() {
		t.Fatal("xayriya belgisi")
	}

	id, ok, err := s.AddCharityPayout(ctx, store.CharityPayout{Kind: store.CharityMonthly, Period: "2026-10", Share: 15, Donated: 4, Stars: 19})
	if err != nil || !ok || id == 0 {
		t.Fatal(id, ok, err)
	}
	if _, ok, err := s.AddCharityPayout(ctx, store.CharityPayout{Kind: store.CharityMonthly, Period: "2026-10", Stars: 1}); ok || err != nil {
		t.Fatalf("oy ikki marta yozilmasin: %v %v", ok, err)
	}
	at := time.Now()
	mid, ok, err := s.AddCharityPayout(ctx, store.CharityPayout{Kind: store.CharityManual, Stars: 500, Note: "naqd", Paid: true, ByAdmin: 9, PaidAt: &at})
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, ok, _ := s.AddCharityPayout(ctx, store.CharityPayout{Kind: store.CharityManual, Stars: 1, Paid: true, PaidAt: &at}); !ok {
		t.Fatal("manual cheklanmaydi")
	}
	if err := s.MarkCharityPaid(ctx, id, "EQtx", 9, at); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkCharityPaid(ctx, id, "EQtx", 9, at); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ikki marta: %v", err)
	}
	if err := s.MarkCharityPaid(ctx, 999999, "x", 9, at); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	ps, err := s.ListCharityPayouts(ctx, 0)
	if err != nil || len(ps) != 3 || ps[2].ID != id || ps[1].ID != mid {
		t.Fatalf("list: %+v %v", ps, err)
	}
	m := ps[2]
	if !m.Paid || m.Proof != "EQtx" || m.ByAdmin != 9 || m.PaidAt == nil || m.Share != 15 || m.Donated != 4 || m.Stars != 19 || m.Period != "2026-10" {
		t.Fatalf("monthly: %+v", m)
	}
	if ps[1].Note != "naqd" || !ps[1].Paid || ps[1].Kind != store.CharityManual {
		t.Fatalf("manual: %+v", ps[1])
	}
	if ps, _ := s.ListCharityPayouts(ctx, 1); len(ps) != 1 {
		t.Fatalf("limit: %v", ps)
	}
}
