package userbot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
	"github.com/ozodjongg/secretar_ai_agent/internal/miniapp"
	"github.com/ozodjongg/secretar_ai_agent/internal/ratelimit"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// LoginIO — login paytida mijozdan kod/parol olish kanali (Mini App orqali).
type LoginIO interface {
	// Code — via: kod qayerga yuborilgani (app, sms, call, email, fragment), hint: email shabloni va h.k.
	Code(ctx context.Context, via, hint string) (string, error)
	Password(ctx context.Context) (string, error)
	// QR — kodsiz kirish tokeni tayyor (yangilanganda qayta chaqiriladi): tg://login havola, PNG data URL.
	QR(url, image string, expires time.Time)
}

// Connector — Telegram'ga ulanish qatlami (gotd.go'da implementatsiya).
type Connector interface {
	// Login telefon raqam bilan avtorizatsiya qiladi; sessiya shifrlangan holda saqlanadi.
	Login(ctx context.Context, owner int64, phone string, io LoginIO) error
	// LoginQR kodsiz kirish: token mijozning Telegram ilovasida tasdiqlanadi.
	// Tasdiqlangan akkaunt owner'dan boshqa bo'lsa — ErrWrongAccount.
	LoginQR(ctx context.Context, owner int64, io LoginIO) error
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
	// CalendarFallback — Google'siz ham uchrashuv taklif qilish (.ics).
	CalendarFallback bool
	// DraftCard — qoralamani bot chatiga tugmalar bilan yuborish (bot).
	DraftCard func(ctx context.Context, d store.Draft, contactName string) error
	// Gate yopiq turguncha hech bir sessiya Telegram'ga ulanmaydi (nil → darhol). Deploy paytida
	// eski va yangi nusxa bitta sessiyaga bir vaqtda ulansa, Telegram AUTH_KEY_DUPLICATED bilan
	// sessiyani butunlay bekor qiladi — yangi nusxa eskisi to'xtashini shu orqali kutadi.
	Gate <-chan struct{}

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
		DraftCard: m.DraftCard, CalendarFallback: m.CalendarFallback,
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
		if m.Gate != nil {
			select {
			case <-m.Gate:
			case <-ctx.Done():
				return
			}
		}
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
			m.markFatal(owner, fatal.Reason)
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
// Worker goroutine'ining o'zidan chaqirmang (u yerda markFatal ishlating).
func (m *Manager) breaker(owner int64, reason string) {
	m.stopWorker(owner)
	m.markFatal(owner, reason)
}

// markFatal holatni sababga qarab belgilaydi (worker'ni to'xtatmaydi).
func (m *Manager) markFatal(owner int64, reason string) {
	if sessionDead[reason] {
		m.markSessionDead(owner, reason)
		return
	}
	m.markBroken(owner, reason)
}

// sessionDead — Telegram sessiyani butunlay bekor qilgan: kutish foydasiz, qayta ulash kerak.
var sessionDead = map[string]bool{
	"AUTH_KEY_UNREGISTERED": true, "AUTH_KEY_DUPLICATED": true, "SESSION_REVOKED": true, "SESSION_EXPIRED": true,
}

// markSessionDead akkauntni "revoked"ga o'tkazadi — Mini App qayta ulash oynasini ko'rsatadi.
func (m *Manager) markSessionDead(owner int64, reason string) {
	ctx := context.WithoutCancel(m.BaseCtx)
	_ = m.Store.SetStatus(ctx, owner, store.StatusRevoked, "Telegram sessiyani bekor qildi ("+reason+")")
	m.Store.Audit(ctx, owner, "circuit_breaker", reason)
	if m.Notify != nil {
		m.Notify(ctx, owner, "Telegram kotib sessiyasini bekor qildi ("+reason+"). Kutish yordam bermaydi — "+
			"ilovada Telegram akkauntingizni qayta ulang (telefon raqam va kod).", true)
	}
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

// ErrDraftGone — qoralama topilmadi yoki allaqachon hal qilingan.
var ErrDraftGone = fmt.Errorf("qoralama allaqachon hal qilingan: %w", store.ErrNotFound)

// ErrDraftTooLong — tahrirlangan matn Telegram limitidan uzun.
var ErrDraftTooLong = errors.New("matn juda uzun")

// SendDraftByID kutilayotgan qoralamani yuboradi (bot tugmasi yoki bot xabariga javob).
// text bo'sh bo'lmasa — qoralama o'rniga shu matn yuboriladi. Holat avval "sent"ga o'tadi
// (ikki marta bosish ikki marta yubormaydi); yuborilmasa qayta "pending"ga qaytadi.
func (m *Manager) SendDraftByID(ctx context.Context, owner, id int64, text string) error {
	d, err := m.Store.GetDraft(ctx, owner, id)
	if err != nil || d.Status != store.DraftPending {
		return ErrDraftGone
	}
	if t := strings.TrimSpace(text); t != "" {
		if utf8.RuneCountInString(t) > 4000 {
			return ErrDraftTooLong
		}
		d.Reply = t
	}
	if err := m.Store.ResolveDraft(ctx, owner, id, store.DraftSent); err != nil {
		return ErrDraftGone
	}
	if err := m.SendDraft(ctx, owner, d); err != nil {
		if rerr := m.Store.ReopenDraft(context.WithoutCancel(ctx), owner, id); rerr != nil && m.Log != nil {
			m.Log.Error("reopen draft", "owner", owner, "draft", id, "err", rerr)
		}
		return err
	}
	m.Store.Audit(ctx, owner, "draft_sent", strconv.FormatInt(id, 10))
	return nil
}

// RejectDraft qoralamani rad etadi.
func (m *Manager) RejectDraft(ctx context.Context, owner, id int64) error {
	if err := m.Store.ResolveDraft(ctx, owner, id, store.DraftRejected); err != nil {
		return ErrDraftGone
	}
	m.Store.Audit(ctx, owner, "draft_rejected", strconv.FormatInt(id, 10))
	return nil
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
