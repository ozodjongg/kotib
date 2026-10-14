package userbot

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
	"github.com/ozodjongg/secretar_ai_agent/internal/miniapp"
	"github.com/ozodjongg/secretar_ai_agent/internal/ratelimit"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// LoginIO — login paytida mijozdan kod/parol olish kanali (Mini App orqali).
type LoginIO interface {
	Code(ctx context.Context) (string, error)
	Password(ctx context.Context) (string, error)
}

// Connector — Telegram'ga ulanish qatlami (gotd.go'da implementatsiya).
type Connector interface {
	// Login telefon raqam bilan avtorizatsiya qiladi; sessiya shifrlangan holda saqlanadi.
	Login(ctx context.Context, owner int64, phone string, io LoginIO) error
	// Run saqlangan sessiya bilan ulanadi va ctx tugaguncha ishlaydi.
	// onReady ulanish tayyor bo'lganda, onMsg har kiruvchi shaxsiy xabarda chaqiriladi (tez qaytishi shart).
	// Qaytarilgan *FatalError — qayta urinish ma'nosiz.
	Run(ctx context.Context, owner int64, onReady func(Telegram), onMsg func(Incoming)) error
}

type Manager struct {
	Store   store.Store
	Conn    Connector
	LLM     llm.Generator
	Notify  Notifier
	Log     *slog.Logger
	Limits  ratelimit.Config
	BaseCtx context.Context
	// Entitled — obuna tekshiruvi (billing.Service.Entitled); nil → doim ruxsat.
	Entitled func(ctx context.Context, owner int64) bool
	// Quota — sinovdagi kunlik limit (billing.Service.UseQuota); nil → cheklovsiz.
	Quota func(ctx context.Context, owner int64) bool
	// Google — taqvim/kontakt integratsiyasi; nil → o'chiq.
	Google Google
	// Propose — taklif qilingan amalni egasiga tasdiqlash uchun yuborish (bot).
	Propose func(ctx context.Context, owner, actionID int64, text string)

	mu         sync.Mutex
	workers    map[int64]*worker
	logins     map[int64]*loginSession
	loginTimes map[int64][]time.Time
}

type worker struct {
	cancel context.CancelFunc
	done   chan struct{}
	pipe   *Pipeline
	mu     sync.Mutex
	tg     Telegram
}

func (w *worker) telegram() Telegram {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.tg
}

// Start barcha faol (rozilik berilgan) mijozlar uchun sessiyalarni ishga tushiradi.
func (m *Manager) Start(ctx context.Context) error {
	m.init(ctx)
	owners, err := m.Store.ListActiveOwners(ctx)
	if err != nil {
		return err
	}
	for _, o := range owners {
		m.startWorker(o)
		// Hammasi bir vaqtda ulanmasin (bir IP'dan ko'p ulanish shubhali): 2s oraliq.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil
}

func (m *Manager) init(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.workers == nil {
		m.workers = map[int64]*worker{}
		m.logins = map[int64]*loginSession{}
		m.loginTimes = map[int64][]time.Time{}
		m.BaseCtx = ctx
		if m.Log == nil {
			m.Log = slog.Default()
		}
		if m.Limits == (ratelimit.Config{}) {
			m.Limits = ratelimit.DefaultConfig()
		}
	}
}

// Shutdown barcha sessiyalarni to'xtatadi va tugashini kutadi.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	ws := make([]*worker, 0, len(m.workers))
	for _, w := range m.workers {
		ws = append(ws, w)
	}
	m.mu.Unlock()
	for _, w := range ws {
		w.cancel()
	}
	for _, w := range ws {
		<-w.done
	}
}

func (m *Manager) startWorker(owner int64) {
	m.mu.Lock()
	if _, ok := m.workers[owner]; ok {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(m.BaseCtx)
	w := &worker{cancel: cancel, done: make(chan struct{})}
	w.pipe = &Pipeline{
		Owner: owner, Store: m.Store, LLM: m.LLM, Notify: m.Notify, Log: m.Log,
		Limiter: ratelimit.NewLimiter(m.Limits), Entitled: m.Entitled, Quota: m.Quota, Google: m.Google, Propose: m.Propose,
	}
	w.pipe.OnFatal = func(reason string) { go m.breaker(owner, reason) }
	m.workers[owner] = w
	m.mu.Unlock()

	go func() {
		defer close(w.done)
		defer func() {
			m.mu.Lock()
			if m.workers[owner] == w {
				delete(m.workers, owner)
			}
			m.mu.Unlock()
		}()
		m.runLoop(ctx, owner, w)
	}()
}

// runLoop: ulanishni ushlab turadi; vaqtinchalik xatolarda eksponensial kutish bilan qayta ulanadi.
func (m *Manager) runLoop(ctx context.Context, owner int64, w *worker) {
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := m.Conn.Run(ctx, owner,
			func(tg Telegram) {
				w.mu.Lock()
				w.tg = tg
				w.mu.Unlock()
				w.pipe.SetTG(tg)
			},
			func(in Incoming) { w.pipe.Submit(ctx, in) },
		)
		w.mu.Lock()
		w.tg = nil
		w.mu.Unlock()
		w.pipe.SetTG(nil)
		if ctx.Err() != nil {
			return
		}
		var fatal *FatalError
		if errors.As(err, &fatal) {
			// Worker goroutine ichidamiz: stopWorker (o'z done kanalini kutadi) deadlock beradi.
			// Shuning uchun faqat holatni belgilaymiz — runLoop'dan chiqish worker'ni o'zi to'xtatadi.
			m.markBroken(owner, fatal.Reason)
			return
		}
		m.Log.Warn("sessiya uzildi, qayta ulanadi", "owner", owner, "in", backoff)
		if time.Since(started) > 5*time.Minute {
			backoff = 5 * time.Second // uzoq ishlagan — qayta hisoblash
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 5*time.Minute {
			backoff *= 2
		}
	}
}

// breaker — circuit breaker: akkauntni "broken"ga o'tkazadi, sessiyani to'xtatadi, mijozga xabar beradi.
// Avtomatik qayta ishga tushirilmaydi: mijoz sababni ko'rib, qo'lda "Davom ettirish" bosadi.
// Worker goroutine'ining o'zidan chaqirmang (u yerda markBroken ishlating).
func (m *Manager) breaker(owner int64, reason string) {
	m.stopWorker(owner)
	m.markBroken(owner, reason)
}

// markBroken akkauntni "broken" holatiga o'tkazadi va mijozga xabar beradi (worker'ni to'xtatmaydi).
func (m *Manager) markBroken(owner int64, reason string) {
	ctx := context.WithoutCancel(m.BaseCtx)
	_ = m.Store.SetStatus(ctx, owner, store.StatusBroken, reason)
	m.Store.Audit(ctx, owner, "circuit_breaker", reason)
	if m.Notify != nil {
		m.Notify(ctx, owner, "Kotib xavfsizlik uchun to'xtatildi (Telegram cheklov/sessiya muammosi). "+
			"Ilovada holatni tekshiring. Avtomatik qayta yoqilmaydi.", true)
	}
}

func (m *Manager) stopWorker(owner int64) {
	m.mu.Lock()
	w := m.workers[owner]
	m.mu.Unlock()
	if w != nil {
		w.cancel()
		<-w.done
	}
}

// ---- miniapp.Controller ----

var _ miniapp.Controller = (*Manager)(nil)

func (m *Manager) Pause(owner int64) { m.stopWorker(owner) }

func (m *Manager) Resume(ctx context.Context, owner int64) error {
	m.init(ctx)
	if _, err := m.Store.LoadSession(ctx, owner); err != nil {
		return errors.New("avval Telegram akkauntni ulang")
	}
	m.startWorker(owner)
	return nil
}

func (m *Manager) SendDraft(ctx context.Context, owner int64, d store.Draft) error {
	m.mu.Lock()
	w := m.workers[owner]
	m.mu.Unlock()
	if w == nil || w.telegram() == nil {
		return errors.New("sessiya hozir ulanmagan")
	}
	return w.pipe.SendDraft(ctx, d)
}

var ErrNoLiveSession = errors.New("jonli sessiya yo'q")

// Revoke Telegram tomonda sessiyani tugatadi. Jonli ulanish yo'q bo'lsa, mijozga qo'lda uzishni aytadi.
func (m *Manager) Revoke(ctx context.Context, owner int64) error {
	m.init(ctx)
	m.cancelLogin(owner)
	m.mu.Lock()
	w := m.workers[owner]
	m.mu.Unlock()
	var err error
	if w != nil && w.telegram() != nil {
		lctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = w.telegram().LogOut(lctx)
		cancel()
	} else {
		err = ErrNoLiveSession
	}
	m.stopWorker(owner)
	if err != nil && m.Notify != nil {
		m.Notify(ctx, owner, "Ma'lumotlaringiz o'chirildi. Telegram → Sozlamalar → Qurilmalar bo'limidan "+
			"kotib sessiyasini qo'lda uzib qo'ying (avtomatik uzib bo'lmadi).", false)
	}
	return err
}

func (m *Manager) Contacts(ctx context.Context, owner int64) ([]miniapp.ContactInfo, error) {
	m.mu.Lock()
	w := m.workers[owner]
	m.mu.Unlock()
	if w == nil || w.telegram() == nil {
		return nil, ErrNoLiveSession
	}
	return w.telegram().Contacts(ctx)
}
