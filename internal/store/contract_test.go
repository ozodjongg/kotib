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
		if _, err := db.Exec(`TRUNCATE accounts, subscriptions, payments, usage_daily, google_links, actions CASCADE`); err != nil {
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

	if err := s.DeleteAccount(ctx, 1); err != nil {
		t.Fatal(err)
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
}
