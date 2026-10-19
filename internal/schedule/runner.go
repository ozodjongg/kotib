package schedule

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
	"github.com/ozodjongg/secretar_ai_agent/internal/plugin"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

const (
	runTimeout    = 3 * time.Minute
	maxConcurrent = 4
	dueBatch      = 50
)

var (
	ErrBusy       = errors.New("vazifa hozir bajarilmoqda — natijani kuting")
	ErrNotEntitle = errors.New("obuna faol emas — rejalashtirilgan vazifalar to'xtatilgan")
	ErrDailyLimit = errors.New("bugungi bajarilishlar chegarasiga yetildi — ertaga davom etadi")
)

// Runner muddati kelgan vazifalarni bajaradi. Faqat bitta nusxa (gate) ishlatadi; bir vazifani
// ikki marta bajarmaslik uchun har biri bazada atomar egallanadi (ClaimSchedule).
type Runner struct {
	Store   store.Store
	AI      llm.TaskRunner
	Plugins *plugin.Registry
	Vault   plugin.Vault
	// Notify natijani egasiga bot orqali yuboradi.
	Notify func(ctx context.Context, owner int64, text string)
	// Deliver natijani egasining kontaktiga yuboradi (userbot.Manager.Deliver): sent=false — qoralama
	// yaratildi (egasiga xabar boradi). nil → kontaktga yuborish o'chiq, natija faqat egasiga.
	Deliver func(ctx context.Context, owner, chatID int64, name, title, text string, auto bool) (sent bool, err error)
	// Entitled — obuna faolmi (billing.Service.Entitled); nil → doim.
	Entitled func(ctx context.Context, owner int64) bool
	// Pro — plaginlar ochiqmi (billing.Service.HasPro); nil → ochiq. Pro bo'lmasa vazifa plaginsiz bajariladi.
	Pro func(ctx context.Context, owner int64) bool
	Log *slog.Logger
	// Now — test uchun soat; nil → time.Now.
	Now func() time.Time

	once sync.Once
	sem  chan struct{}
	wg   sync.WaitGroup
}

func (r *Runner) init() {
	r.once.Do(func() {
		r.sem = make(chan struct{}, maxConcurrent)
		if r.Log == nil {
			r.Log = slog.Default()
		}
	})
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) location(ctx context.Context, owner int64) *time.Location {
	if st, err := r.Store.GetSettings(ctx, owner); err == nil {
		return st.Location()
	}
	return time.UTC
}

// Loop har 30 soniyada muddati kelgan vazifalarni tekshiradi (gate ochilgach).
func (r *Runner) Loop(ctx context.Context, gate <-chan struct{}) {
	select {
	case <-gate:
	case <-ctx.Done():
		return
	}
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		r.Tick(ctx)
		select {
		case <-ctx.Done():
			r.wg.Wait()
			return
		case <-t.C:
		}
	}
}

// Tick muddati kelgan vazifalarni egallaydi va fonda bajaradi. Server o'chiq turgan paytda
// o'tib ketgan takrorlar yig'ilmaydi: vazifa bir marta bajariladi, keyingisi hozirdan hisoblanadi.
func (r *Runner) Tick(ctx context.Context) {
	r.init()
	now := r.now()
	due, err := r.Store.ListDueSchedules(ctx, now, dueBatch)
	if err != nil {
		r.Log.Warn("schedule due", "err", err)
		return
	}
	for _, s := range due {
		var next *time.Time
		if s.Kind != store.ScheduleOnce {
			next = Next(s, now, r.location(ctx, s.OwnerID))
		}
		ok, err := r.Store.ClaimSchedule(ctx, s.OwnerID, s.ID, *s.NextRun, next, now)
		if err != nil || !ok {
			continue // boshqa oqim egalladi yoki baza xatosi — keyingi tick
		}
		if err := r.allowed(ctx, s.OwnerID, now); err != nil {
			_ = r.Store.FinishSchedule(ctx, s.OwnerID, s.ID, "error", err.Error())
			continue
		}
		r.spawn(ctx, s)
	}
}

// RunNow — «Hozir bajarish»: jadvalga tegmasdan vazifani darhol fonda bajaradi.
func (r *Runner) RunNow(ctx context.Context, owner, id int64) error {
	r.init()
	s, err := r.Store.GetSchedule(ctx, owner, id)
	if err != nil {
		return err
	}
	now := r.now()
	if err := r.allowed(ctx, owner, now); err != nil {
		return err
	}
	ok, err := r.Store.MarkScheduleRunning(ctx, owner, id, now)
	if err != nil {
		return err
	}
	if !ok {
		return ErrBusy
	}
	r.spawn(context.WithoutCancel(ctx), s)
	return nil
}

func (r *Runner) allowed(ctx context.Context, owner int64, now time.Time) error {
	if r.Entitled != nil && !r.Entitled(ctx, owner) {
		return ErrNotEntitle
	}
	loc := r.location(ctx, owner)
	l := now.In(loc)
	day := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
	if n, err := r.Store.CountAudit(ctx, owner, []string{"schedule_run"}, day); err != nil || n >= MaxRunsPerDay {
		return ErrDailyLimit
	}
	return nil
}

func (r *Runner) spawn(ctx context.Context, s store.Schedule) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		select {
		case r.sem <- struct{}{}:
		case <-ctx.Done():
			_ = r.Store.FinishSchedule(context.WithoutCancel(ctx), s.OwnerID, s.ID, "error", "server to'xtadi")
			return
		}
		defer func() { <-r.sem }()
		r.execute(ctx, s)
	}()
}

// Wait fonda bajarilayotgan vazifalar tugashini kutadi (test va to'xtatish uchun).
func (r *Runner) Wait() { r.wg.Wait() }

func (r *Runner) execute(ctx context.Context, s store.Schedule) {
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	bg := context.WithoutCancel(ctx)
	r.Store.Audit(bg, s.OwnerID, "schedule_run", s.Title)

	st, _ := r.Store.GetSettings(ctx, s.OwnerID)
	loc := st.Location()
	req := llm.TaskRequest{OwnerName: st.OwnerName, Title: s.Title, Prompt: s.Prompt, Now: r.now().In(loc)}
	if s.TargetID != 0 && r.Deliver != nil {
		req.Recipient = s.TargetName
		if req.Recipient == "" {
			req.Recipient = "kontakt"
		}
	}
	if r.Plugins != nil && r.Vault != nil && (r.Pro == nil || r.Pro(ctx, s.OwnerID)) {
		req.Tools, req.Call = r.Plugins.Toolset(r.Plugins.EnabledFor(ctx, r.Store, r.Vault, s.OwnerID))
	}
	res, err := r.AI.RunTask(ctx, req)
	if err != nil {
		r.Log.Warn("schedule run", "owner", s.OwnerID, "schedule", s.ID, "err", err)
		msg := "AI javob bermadi. Keyinroq qayta urinib ko'ring yoki ko'rsatmani soddalashtiring."
		_ = r.Store.FinishSchedule(bg, s.OwnerID, s.ID, "error", msg)
		r.notify(bg, s.OwnerID, "⚠️ «"+s.Title+"» bajarilmadi: "+msg)
		return
	}
	text := clip(strings.TrimSpace(res.Text), MaxResult)
	_ = r.Store.FinishSchedule(bg, s.OwnerID, s.ID, "ok", text)
	if s.TargetID != 0 && r.Deliver != nil {
		r.deliver(bg, s, text)
		return
	}
	r.notify(bg, s.OwnerID, "⏰ "+s.Title+"\n\n"+text)
}

// deliver natijani vazifada tanlangan kontaktga yuboradi (Pro). Natija egasiga ham ma'lum qilinadi.
func (r *Runner) deliver(ctx context.Context, s store.Schedule, text string) {
	name := s.TargetName
	if name == "" {
		name = "kontakt"
	}
	if r.Pro != nil && !r.Pro(ctx, s.OwnerID) {
		r.notify(ctx, s.OwnerID, "⏰ "+s.Title+"\n\n"+text+"\n\nℹ️ Kontaktga yuborish Pro tarifda — natija faqat sizga keldi.")
		return
	}
	sent, err := r.Deliver(ctx, s.OwnerID, s.TargetID, s.TargetName, s.Title, text, s.TargetAuto)
	switch {
	case err != nil:
		r.Log.Warn("schedule deliver", "owner", s.OwnerID, "schedule", s.ID, "err", err)
		r.notify(ctx, s.OwnerID, "⏰ "+s.Title+"\n\n"+text+"\n\n⚠️ "+name+"ga yuborilmadi: "+err.Error())
	case sent:
		r.notify(ctx, s.OwnerID, "⏰ "+s.Title+" — "+name+"ga yuborildi ✅\n\n"+text)
	}
	// Qoralama bo'lsa — egasiga qoralama haqida xabar Deliver ichida boradi.
}

func (r *Runner) notify(ctx context.Context, owner int64, text string) {
	if r.Notify != nil {
		r.Notify(ctx, owner, text)
	}
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
