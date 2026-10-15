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
	t.Run("memory", func(t *testing.T) { contract(t, store.NewMemory()) })
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
		if _, err := db.Exec(`TRUNCATE accounts, subscriptions, payments, usage_daily, google_links, actions, hooks, followups, referral_codes, referrals, linked_accounts, link_tokens, chat_activity, style_samples CASCADE`); err != nil {
			t.Fatal(err)
		}
		contract(t, store.NewPostgres(db))
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
