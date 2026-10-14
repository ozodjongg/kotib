// Package userbot: mijoz akkaunti nomidan kiruvchi xabarlarni qayta ishlash.
//
// Bu fayl Telegram kutubxonasidan MUSTAQIL (Telegram interfeysi orqali) — shuning
// uchun gotd'siz to'liq test qilinadi. gotd bilan ulanish gotd.go (build tag: gotd) da.
package userbot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/google"
	"github.com/ozodjongg/secretar_ai_agent/internal/guard"
	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
	"github.com/ozodjongg/secretar_ai_agent/internal/miniapp"
	"github.com/ozodjongg/secretar_ai_agent/internal/ratelimit"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// Telegram — bitta mijoz sessiyasining ish yuzasi.
type Telegram interface {
	// SendReply xabarni yuboradi; oldin "typing" holatini ko'rsatib, typing davomida kutadi.
	SendReply(ctx context.Context, chatID int64, replyTo int, text string, typing time.Duration) error
	MarkRead(ctx context.Context, chatID int64, maxID int) error
	// History chatdagi oxirgi n ta matnli xabar (eskidan yangiga).
	History(ctx context.Context, chatID int64, n int) ([]llm.Turn, error)
	// Contacts mijozning Telegram kontaktlari.
	Contacts(ctx context.Context) ([]miniapp.ContactInfo, error)
	// LogOut Telegram tomonda shu sessiyani tugatadi (auth.logOut).
	LogOut(ctx context.Context) error
}

// Google — mijozning Google akkaunti (google.Client).
type Google interface {
	Connected(ctx context.Context, owner int64) bool
	Busy(ctx context.Context, owner int64, from, to time.Time) ([]llm.Interval, error)
	Contact(ctx context.Context, owner, userID int64, name, phone string) (string, string)
}

// googleTimeout — Google kontekstini yig'ish uchun vaqt (model vaqtini yeb qo'ymasin).
const googleTimeout = 8 * time.Second

// busyHorizon — kotibga taqvimning necha kunlik band vaqtlari ko'rsatiladi.
const busyHorizon = 7 * 24 * time.Hour

// Incoming — kiruvchi shaxsiy xabar.
type Incoming struct {
	ChatID  int64
	MsgID   int
	Text    string
	Contact config.Contact
}

// FatalError — qayta urinish ma'nosiz (sessiya o'chgan, PEER_FLOOD, akkaunt bloklangan).
type FatalError struct{ Reason string }

func (e *FatalError) Error() string { return "fatal: " + e.Reason }

// FloodWaitError — Telegram "X soniya kut" dedi. Hurmat qilamiz: kutamiz, qayta urinmaymiz.
type FloodWaitError struct{ Wait time.Duration }

func (e *FloodWaitError) Error() string { return fmt.Sprintf("flood wait %s", e.Wait) }

var ErrRateLimited = errors.New("tezlik limiti")

// maxPendingChats — debounce navbatidagi chatlar soni chegarasi (xotira/spam himoyasi).
const maxPendingChats = 200

type Notifier func(ctx context.Context, owner int64, text string, openApp bool)

type Pipeline struct {
	Owner   int64
	Store   store.Store
	LLM     llm.Generator
	TG      Telegram
	Notify  Notifier
	Limiter *ratelimit.Limiter
	Log     *slog.Logger
	OnFatal func(reason string) // circuit breaker: manager akkauntni "broken"ga o'tkazadi
	// Entitled — obuna/sinov faolmi (nil → doim ha). Faol bo'lmasa model chaqirilmaydi.
	Entitled func(ctx context.Context, owner int64) bool
	// Quota — kunlik limit (sinovda); har model chaqiruvidan oldin, false → chaqirilmaydi.
	// nil → cheklovsiz.
	Quota func(ctx context.Context, owner int64) bool
	// Google — taqvim/kontakt konteksti (nil → integratsiya o'chiq).
	Google Google
	// Propose — kotib taklif qilgan amalni egasiga tasdiqlash uchun yuboradi (bot tugmalari).
	Propose func(ctx context.Context, owner, actionID int64, text string)
	// DraftCard — qoralamani bot chatiga tugmalar bilan yuboradi (Settings.DraftsInBot).
	DraftCard func(ctx context.Context, d store.Draft, contactName string) error
	Debounce  time.Duration // ketma-ket xabarlarni birlashtirish oynasi (default 4s)

	// testda almashtiriladi
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error

	sem          chan struct{}     // bir vaqtda LLM chaqiruvlari cheklovi
	proposals    *ratelimit.Bucket // egasiga yuboriladigan takliflar (bot xabarlari) cheklovi
	tgMu         sync.RWMutex
	mu           sync.Mutex
	pending      map[int64]*pendingChat
	blockedUntil time.Time
	notifiedAt   map[string]time.Time
	wg           sync.WaitGroup
}

type pendingChat struct {
	texts   []string
	lastID  int
	contact config.Contact
	timer   *time.Timer
}

func (p *Pipeline) init() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		p.pending = map[int64]*pendingChat{}
		p.notifiedAt = map[string]time.Time{}
		p.sem = make(chan struct{}, 3)
		if p.Debounce == 0 {
			p.Debounce = 4 * time.Second
		}
		if p.Now == nil {
			p.Now = time.Now
		}
		if p.Sleep == nil {
			p.Sleep = sleepCtx
		}
		if p.Log == nil {
			p.Log = slog.Default()
		}
		// Kontakt ko'p xabar yozsa ham egasi takliflarga ko'milib ketmasin: 5 ta, keyin ~10 daqiqada bitta.
		p.proposals = ratelimit.NewBucket(5, 1.0/600)
	}
}

// SetTG ulanish (qayta) o'rnatilganda Telegram sessiyasini almashtiradi.
func (p *Pipeline) SetTG(t Telegram) {
	p.tgMu.Lock()
	p.TG = t
	p.tgMu.Unlock()
}

func (p *Pipeline) tg() Telegram {
	p.tgMu.RLock()
	defer p.tgMu.RUnlock()
	return p.TG
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Submit kiruvchi xabarni navbatga qo'yadi. Bir chatdan Debounce ichida kelgan xabarlar
// birlashtiriladi: 5 ta xabarga 5 ta javob emas, bitta javob.
func (p *Pipeline) Submit(ctx context.Context, in Incoming) {
	p.init()
	p.mu.Lock()
	defer p.mu.Unlock()
	pc := p.pending[in.ChatID]
	if pc == nil {
		if len(p.pending) >= maxPendingChats {
			// Ko'p chatdan bir vaqtda xabar yog'ilsa (spam to'lqini) — ortiqchasini tashlaymiz.
			p.Log.Warn("pending chatlar limiti", "owner", p.Owner)
			return
		}
		pc = &pendingChat{}
		p.pending[in.ChatID] = pc
	}
	if len(pc.texts) < 10 { // haddan ortiq xabar to'plamaymiz (spam/xotira)
		pc.texts = append(pc.texts, in.Text)
	}
	pc.lastID, pc.contact = in.MsgID, in.Contact
	if pc.timer != nil && pc.timer.Stop() {
		p.wg.Done() // bekor qilingan eski taymerning hisobi
	}
	chatID := in.ChatID
	p.wg.Add(1)
	pc.timer = time.AfterFunc(p.Debounce, func() {
		defer p.wg.Done()
		p.mu.Lock()
		cur := p.pending[chatID]
		delete(p.pending, chatID)
		p.mu.Unlock()
		if cur == nil {
			return
		}
		p.Handle(ctx, Incoming{ChatID: chatID, MsgID: cur.lastID, Text: strings.Join(cur.texts, "\n"), Contact: cur.contact})
	})
}

// Wait testlar uchun: barcha kutilayotgan ishlar tugashini kutadi.
func (p *Pipeline) Wait() { p.wg.Wait() }

// Handle bitta (birlashtirilgan) kiruvchi xabarni to'liq qayta ishlaydi.
func (p *Pipeline) Handle(ctx context.Context, in Incoming) {
	p.init()
	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	case <-ctx.Done():
		return
	}

	tg := p.tg()
	if tg == nil {
		return // sessiya hali ulanmagan
	}
	st, err := p.Store.GetSettings(ctx, p.Owner)
	if err != nil {
		p.Log.Error("settings", "owner", p.Owner, "err", err)
		return
	}
	now := p.Now()
	dec := st.Resolve(in.Contact, now)
	if !dec.Reply {
		p.Store.Audit(ctx, p.Owner, "skip", dec.Reason)
		return
	}

	if p.Entitled != nil && !p.Entitled(ctx, p.Owner) {
		p.Store.Audit(ctx, p.Owner, "skip", "obuna faol emas")
		p.notifyOnce(ctx, "billing", 24*time.Hour,
			"Obuna faol emas — kotib yangi xabarlarga javob tayyorlamayapti. Davom ettirish uchun ilovada obunani yoqing.", true)
		return
	}

	forced := "" // bo'sh bo'lmasa: avtomatik yuborish taqiqlangan, sabab shu
	verdict := guard.ClassifyIncoming(in.Text)
	switch verdict.Verdict {
	case guard.Block:
		p.Store.Audit(ctx, p.Owner, "blocked", verdict.Reason)
		p.notifyOnce(ctx, fmt.Sprintf("block:%d", in.ChatID), time.Hour,
			"Bir kontakt xabariga avtomatik javob yozilmadi (xavfsizlik filtri). O'zingiz ko'rib chiqing.", false)
		return
	case guard.Confirm:
		forced = "sezgir mavzu: " + verdict.Reason // hech qachon avtomatik emas
	}
	if forced == "" && in.Contact.IsBot {
		forced = "bot — faqat qoralama" // ikkinchi himoya qatlami (config.Resolve ham draft qiladi)
	}
	if forced == "" && now.Before(p.blockedUntilTime()) {
		forced = "Telegram kutishni so'radi (FloodWait)" // kutamiz, faqat draft tayyorlaymiz
	}

	if p.Quota != nil && !p.Quota(ctx, p.Owner) {
		p.Store.Audit(ctx, p.Owner, "skip", "sinov kunlik limiti")
		p.notifyOnce(ctx, "quota", 12*time.Hour,
			"Bepul sinovda bugungi javoblar limiti tugadi — kotib ertagacha yangi xabarlarga javob tayyorlamaydi. Obuna bo'lsangiz, cheklov yo'qoladi.", true)
		return
	}

	hist, err := tg.History(ctx, in.ChatID, 8)
	if err != nil {
		p.handleTGErr(ctx, err)
		hist = nil // tarixsiz ham javob yozish mumkin
	}
	// Oxirgi kontakt xabarlari (Incoming) tarixda takrorlanmasin.
	hist = trimTrailingContact(hist, in.Text)

	// Model sekin javob berishi va bir marta qayta urinish uchun yetarli, lekin cheksiz emas.
	lctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	req := llm.Request{
		OwnerName: st.OwnerName, Persona: dec.Persona, Language: st.Language,
		History: hist, Incoming: in.Text, Away: st.Away,
	}
	var resp llm.Response
	if asst, ok := p.LLM.(llm.Assistant); ok && p.Google != nil && p.Google.Connected(ctx, p.Owner) {
		req.Assist = p.assistContext(lctx, &st, in)
		resp, err = asst.Assist(lctx, req)
	} else {
		resp.Text, err = p.LLM.Reply(lctx, req)
	}
	reply := resp.Text
	if err != nil {
		p.Store.Audit(ctx, p.Owner, "llm_error", "")
		p.Log.Warn("llm", "owner", p.Owner, "err", err)
		return
	}

	p.propose(ctx, in, req.Assist, resp)

	if ok, why := guard.CheckOutput(reply, st.AllowLinks); !ok {
		p.Store.Audit(ctx, p.Owner, "output_rejected", why)
		p.makeDraft(ctx, in, reply, "javob filtridan o'tmadi: "+why)
		return
	}

	switch {
	case dec.Mode == config.ModeAuto && forced == "":
		p.sendAuto(ctx, tg, in, reply)
	case forced != "":
		p.makeDraft(ctx, in, reply, forced)
	default:
		p.makeDraft(ctx, in, reply, "draft rejimi")
	}
}

func (p *Pipeline) sendAuto(ctx context.Context, tg Telegram, in Incoming, reply string) {
	if !p.Limiter.Allow(in.ChatID) {
		p.makeDraft(ctx, in, reply, "tezlik limiti")
		return
	}
	read, typing := ratelimit.HumanDelay(in.Text, reply)
	if err := p.Sleep(ctx, read); err != nil {
		return
	}
	if err := tg.MarkRead(ctx, in.ChatID, in.MsgID); err != nil {
		p.handleTGErr(ctx, err)
	}
	if err := tg.SendReply(ctx, in.ChatID, in.MsgID, reply, typing); err != nil {
		p.handleTGErr(ctx, err)
		p.makeDraft(ctx, in, reply, "yuborilmadi")
		return
	}
	p.Store.Audit(ctx, p.Owner, "auto_sent", fmt.Sprintf("chat=%d", in.ChatID))
}

// SendDraft mijoz tasdiqlagan draftni yuboradi. Limit mijoz harakatida ham amal qiladi.
func (p *Pipeline) SendDraft(ctx context.Context, d store.Draft) error {
	p.init()
	if now := p.Now(); now.Before(p.blockedUntilTime()) {
		return &FloodWaitError{Wait: p.blockedUntilTime().Sub(now)}
	}
	if !p.Limiter.Allow(d.ChatID) {
		return ErrRateLimited
	}
	tg := p.tg()
	if tg == nil {
		return errors.New("sessiya ulanmagan")
	}
	_, typing := ratelimit.HumanDelay("", d.Reply)
	if err := tg.SendReply(ctx, d.ChatID, d.ReplyToMsg, d.Reply, typing); err != nil {
		p.handleTGErr(ctx, err)
		return err
	}
	return nil
}

func (p *Pipeline) makeDraft(ctx context.Context, in Incoming, reply, reason string) {
	if reply == "" {
		return
	}
	id, err := p.Store.CreateDraft(ctx, store.Draft{
		OwnerID: p.Owner, ChatID: in.ChatID, ReplyToMsg: in.MsgID,
		Incoming: in.Text, Reply: reply, Reason: reason,
	})
	if err != nil {
		p.Log.Error("create draft", "owner", p.Owner, "err", err)
		return
	}
	p.Store.Audit(ctx, p.Owner, "draft_created", fmt.Sprintf("id=%d", id))
	if p.DraftCard != nil {
		if st, err := p.Store.GetSettings(ctx, p.Owner); err == nil && st.DraftsInBot {
			d := store.Draft{ID: id, OwnerID: p.Owner, ChatID: in.ChatID, Incoming: in.Text, Reply: reply, Reason: reason}
			if err := p.DraftCard(ctx, d, in.Contact.Name); err == nil {
				return
			} else {
				p.Log.Warn("draft card", "owner", p.Owner, "err", err)
			}
		}
	}
	text := "Yangi javob qoralamasi tayyor. Ko'rib chiqing."
	if ds, err := p.Store.ListDrafts(ctx, p.Owner, store.DraftPending, 100); err == nil && len(ds) > 1 {
		text = fmt.Sprintf("%d ta javob qoralamasi sizni kutmoqda.", len(ds))
	}
	p.notifyOnce(ctx, "draft", 30*time.Second, text, true)
}

// handleTGErr — circuit breaker: FloodWait'ni hurmat qiladi, fatal xatoda to'xtaydi.
func (p *Pipeline) handleTGErr(ctx context.Context, err error) {
	var fw *FloodWaitError
	var fatal *FatalError
	switch {
	case errors.As(err, &fw):
		p.mu.Lock()
		if until := p.Now().Add(fw.Wait + 5*time.Second); until.After(p.blockedUntil) {
			p.blockedUntil = until
		}
		p.mu.Unlock()
		p.Store.Audit(ctx, p.Owner, "flood_wait", fw.Wait.String())
	case errors.As(err, &fatal):
		p.Store.Audit(ctx, p.Owner, "fatal", fatal.Reason)
		if p.OnFatal != nil {
			p.OnFatal(fatal.Reason)
		}
	default:
		p.Log.Warn("telegram", "owner", p.Owner, "err", err)
	}
}

func (p *Pipeline) blockedUntilTime() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.blockedUntil
}

func (p *Pipeline) notifyOnce(ctx context.Context, key string, every time.Duration, text string, openApp bool) {
	if p.Notify == nil {
		return
	}
	p.mu.Lock()
	last := p.notifiedAt[key]
	now := p.Now()
	if now.Sub(last) < every {
		p.mu.Unlock()
		return
	}
	p.notifiedAt[key] = now
	p.mu.Unlock()
	p.Notify(ctx, p.Owner, text, openApp)
}

// assistContext Google'dan vaqt, band vaqtlar va kontakt eslatmasini yig'adi. Google
// xatolari javobni to'xtatmaydi — kontekstsiz davom etamiz.
func (p *Pipeline) assistContext(ctx context.Context, st *config.Settings, in Incoming) *llm.Assist {
	ctx, cancel := context.WithTimeout(ctx, googleTimeout)
	defer cancel()
	loc := st.Location()
	now := p.Now().In(loc)
	a := &llm.Assist{Now: now, Loc: loc}
	if busy, err := p.Google.Busy(ctx, p.Owner, now, now.Add(busyHorizon)); err == nil {
		a.Busy = busy
	} else {
		p.Log.Warn("google busy", "owner", p.Owner, "err", err)
		if errors.Is(err, google.ErrNotConnected) {
			p.notifyOnce(ctx, "google", 24*time.Hour,
				"Google ulanishi uzildi (ruxsat bekor qilingan). Taqvim bilan ishlash uchun ilovada Google'ni qayta ulang.", true)
		}
	}
	if in.Contact.IsContact {
		a.ContactName, a.ContactNote = p.Google.Contact(ctx, p.Owner, in.ChatID, in.Contact.Name, in.Contact.Phone)
	}
	return a
}

// propose model taklif qilgan tadbir/vazifani saqlaydi va egasidan tasdiq so'raydi.
// Google'ga hech narsa yozilmaydi — faqat egasi tugmani bossa (botcmd).
func (p *Pipeline) propose(ctx context.Context, in Incoming, a *llm.Assist, resp llm.Response) {
	if a == nil || p.Propose == nil {
		return
	}
	with := a.ContactName
	if with == "" {
		with = in.Contact.Name
	}
	var acts []store.Action
	// Nomlar kontakt xabaridan kelib chiqadi: havola/raqam bo'lsa taklif qilinmaydi.
	if resp.Event != nil && !titleOK(resp.Event.Title) {
		resp.Event = nil
	}
	if resp.Task != nil && !titleOK(resp.Task.Title) {
		resp.Task = nil
	}
	if resp.Event != nil {
		if b, err := google.NewEventPayload(*resp.Event, with); err == nil {
			acts = append(acts, store.Action{OwnerID: p.Owner, Kind: store.ActionEvent, Payload: b})
		}
	}
	if resp.Task != nil {
		if b, err := google.NewTaskPayload(*resp.Task, with); err == nil {
			acts = append(acts, store.Action{OwnerID: p.Owner, Kind: store.ActionTask, Payload: b})
		}
	}
	for _, act := range acts {
		if !p.proposals.Allow() {
			p.Store.Audit(ctx, p.Owner, "skip", "takliflar limiti")
			return
		}
		id, err := p.Store.CreateAction(ctx, act)
		if err != nil {
			p.Log.Warn("action", "owner", p.Owner, "err", err)
			continue
		}
		act.ID = id
		p.Store.Audit(ctx, p.Owner, "action_proposed", string(act.Kind))
		p.Propose(ctx, p.Owner, id, google.Describe(act))
	}
}

func titleOK(t string) bool {
	ok, _ := guard.CheckOutput(t, false)
	return ok
}

// trimTrailingContact tarixning oxiridagi kontakt xabarlarini olib tashlaydi (ular Incoming'da bor).
func trimTrailingContact(h []llm.Turn, incoming string) []llm.Turn {
	for len(h) > 0 && !h[len(h)-1].FromOwner && strings.Contains(incoming, h[len(h)-1].Text) {
		h = h[:len(h)-1]
	}
	return h
}
