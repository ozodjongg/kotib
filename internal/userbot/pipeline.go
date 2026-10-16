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
	// RecentChats oxirgi faol shaxsiy chatlar (botlarsiz, yangidan eskiga, ko'pi bilan n ta).
	RecentChats(ctx context.Context, n int) ([]miniapp.ContactInfo, error)
	// LogOut Telegram tomonda shu sessiyani tugatadi (auth.logOut).
	LogOut(ctx context.Context) error
}

// Google — mijozning Google akkaunti (google.Client).
type Google interface {
	Connected(ctx context.Context, owner int64) bool
	Busy(ctx context.Context, owner int64, from, to time.Time) ([]llm.Interval, error)
	Contact(ctx context.Context, owner, userID int64, name, phone string) (string, string)
}

// defaultHistory — modelga beriladigan oxirgi xabarlar soni (HistoryLimit berilmasa).
const defaultHistory = 8

// chatLock — bitta chat uchun navbat; refs 0 bo'lsa xaritadan o'chiriladi (xotira o'smasin).
type chatLock struct {
	mu   sync.Mutex
	refs int
}

func (p *Pipeline) lockChat(chatID int64) func() {
	p.mu.Lock()
	l := p.chatMu[chatID]
	if l == nil {
		l = &chatLock{}
		p.chatMu[chatID] = l
	}
	l.refs++
	p.mu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		p.mu.Lock()
		if l.refs--; l.refs == 0 {
			delete(p.chatMu, chatID)
		}
		p.mu.Unlock()
	}
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
	Voice   *Voice // ovozli xabar (Text — "🎤 Ovozli xabar"); nil — oddiy matn
	Photo   *Photo // rasm (Text — "🖼 Rasm" + izoh); nil — yo'q
	// Outgoing — egasining o'zi (boshqa qurilmadan) yozgan xabari: javob yozilmaydi,
	// faqat faollik va o'qitish rejimi uchun qayd etiladi.
	Outgoing bool
}

// Photo — rasm: fayl faqat kerak bo'lganda (sozlama yoqilgan bo'lsa) yuklanadi.
type Photo struct {
	Load func(ctx context.Context) ([]byte, error)
}

// PhotoText — rasm o'rnida ko'rsatiladigan matn.
const PhotoText = "🖼 Rasm"

// Voice — ovozli xabar: fayl faqat kerak bo'lganda (sozlama yoqilgan bo'lsa) yuklanadi.
type Voice struct {
	Duration int // soniya
	Load     func(ctx context.Context) ([]byte, error)
}

// VoiceText — ovozli xabar o'rnida ko'rsatiladigan matn (qoralama, bot kartasi).
const VoiceText = "🎤 Ovozli xabar"

// maxVoiceSeconds — bundan uzun ovozli xabarlar modelga yuborilmaydi (xarajat).
const maxVoiceSeconds = 120

// maxTranscribeSeconds — bundan uzun ovozli xabarlar matnga o'girilmaydi.
const maxTranscribeSeconds = 300

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
	// HistoryLimit — modelga beriladigan oxirgi xabarlar soni (obuna/sinovga qarab); nil → defaultHistory.
	HistoryLimit func(ctx context.Context, owner int64) int
	// Quota — kunlik limit (sinovda); har model chaqiruvidan oldin, false → chaqirilmaydi.
	// nil → cheklovsiz.
	Quota func(ctx context.Context, owner int64) bool
	// Google — taqvim/kontakt konteksti (nil → integratsiya o'chiq).
	Google Google
	// Propose — kotib taklif qilgan amalni egasiga tasdiqlash uchun yuboradi (bot tugmalari).
	Propose func(ctx context.Context, owner, actionID int64, text string)
	// CalendarFallback — Google ulanmagan mijozlarga ham uchrashuv taklif qilinadi
	// (tasdiqlansa .ics havola). false → takliflar faqat Google bilan.
	CalendarFallback bool
	// DraftCard — qoralamani bot chatiga tugmalar bilan yuboradi (Settings.DraftsInBot).
	DraftCard func(ctx context.Context, d store.Draft, contactName string) error
	Debounce  time.Duration // ketma-ket xabarlarni birlashtirish oynasi (default 4s)
	// Pro — Pro tarif funksiyalari (ovozli xabarlar, Google) ochiqmi; nil → ochiq.
	Pro func(ctx context.Context, owner int64) bool

	// testda almashtiriladi
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error

	sem          chan struct{} // bir vaqtda LLM chaqiruvlari cheklovi
	shadow       map[int64]shadowGuess
	ownSent      map[int64][]sentMark // kotib yuborgan xabarlar (egasining xabari deb qayta qayd etilmasin)
	proposals    *ratelimit.Bucket    // egasiga yuboriladigan takliflar (bot xabarlari) cheklovi
	chatMu       map[int64]*chatLock  // bitta chat xabarlari qat'iy navbat bilan
	tgMu         sync.RWMutex
	mu           sync.Mutex
	pending      map[int64]*pendingChat
	blockedUntil time.Time
	notifiedAt   map[string]time.Time
	wg           sync.WaitGroup
}

// shadowGuess — soya rejimida kotib jim tayyorlagan javob (egasi yozgach solishtiriladi).
type shadowGuess struct {
	incoming, reply string
	at              time.Time
}

type sentMark struct {
	hash string
	at   time.Time
}

const (
	// shadowTTL — soya taxmini shuncha vaqt ichida egasining javobi bilan solishtiriladi.
	shadowTTL = 12 * time.Hour
	// maxShadow — xotirada saqlanadigan soya taxminlari chegarasi.
	maxShadow = 200
	// ownSentTTL — kotib yuborgan xabar shuncha vaqt ichida "egasi yozdi" deb hisoblanmaydi.
	ownSentTTL = 10 * time.Minute
)

type pendingChat struct {
	texts   []string
	lastID  int
	photo   *Photo
	voice   *Voice // birlashtirilgan xabarlardagi oxirgi ovozli xabar
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
		p.chatMu = map[int64]*chatLock{}
		p.shadow = map[int64]shadowGuess{}
		p.ownSent = map[int64][]sentMark{}
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
	if in.Voice != nil {
		pc.voice = in.Voice
	}
	if in.Photo != nil {
		pc.photo = in.Photo
	}
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
		p.Handle(ctx, Incoming{ChatID: chatID, MsgID: cur.lastID, Text: strings.Join(cur.texts, "\n"), Contact: cur.contact, Voice: cur.voice, Photo: cur.photo})
	})
}

// Wait testlar uchun: barcha kutilayotgan ishlar tugashini kutadi.
func (p *Pipeline) Wait() { p.wg.Wait() }

// Handle bitta (birlashtirilgan) kiruvchi xabarni to'liq qayta ishlaydi.
func (p *Pipeline) Handle(ctx context.Context, in Incoming) {
	p.init()
	// Bitta chatdagi xabarlar navbat bilan: keyingi javob oldingisi yuborilgandan keyin tayyorlanadi
	// va uni tarixda ko'radi (aks holda bir xil javob takrorlanadi, tartib buziladi).
	unlock := p.lockChat(in.ChatID)
	defer unlock()
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
	// Tinch soatlarda ham chat faolligi, shoshilinch ogohlantirish va transkript ishlaydi;
	// egasi qoidalar bilan chetlatgan chat (private) esa umuman qayta ishlanmaydi.
	quiet := !dec.Reply && dec.Reason == config.ReasonQuiet
	p.recordIn(ctx, in, !dec.Reply && !quiet)
	if !dec.Reply && !quiet {
		p.Store.Audit(ctx, p.Owner, "skip", dec.Reason)
		return
	}
	entitled := p.Entitled == nil || p.Entitled(ctx, p.Owner)
	pro := p.Pro == nil || p.Pro(ctx, p.Owner)
	var audio *llm.Audio
	if entitled {
		p.alertUrgent(ctx, &st, in)
		audio = p.transcribe(ctx, &st, in, pro)
	}
	if !dec.Reply {
		p.Store.Audit(ctx, p.Owner, "skip", dec.Reason)
		return
	}

	if !entitled {
		p.Store.Audit(ctx, p.Owner, "skip", "obuna faol emas")
		p.notifyOnce(ctx, "billing", 24*time.Hour,
			"Obuna faol emas — kotib yangi xabarlarga javob tayyorlamayapti. Davom ettirish uchun ilovada obunani yoqing.", true)
		return
	}

	if in.Voice != nil && (!st.ReplyVoice || !pro || in.Voice.Duration > maxVoiceSeconds) {
		// Ovozga javob o'chiq, Pro tarifda emas (yoki juda uzun): faqat matnli qismi bilan davom etamiz.
		in.Voice, audio = nil, nil
		in.Text = strings.TrimSpace(strings.ReplaceAll(in.Text, VoiceText, ""))
		if in.Text == "" {
			p.Store.Audit(ctx, p.Owner, "skip", "ovozli xabar")
			return
		}
	}
	if in.Photo != nil && (!st.ReplyPhotos || !pro) {
		// Rasmga javob o'chiq: izohi bo'lsa — faqat matn bilan.
		in.Photo = nil
		in.Text = strings.TrimSpace(strings.ReplaceAll(in.Text, PhotoText, ""))
		if in.Text == "" {
			p.Store.Audit(ctx, p.Owner, "skip", "rasm")
			return
		}
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
	if forced == "" && in.Voice != nil {
		forced = "ovozli xabar — faqat qoralama" // audio matn filtridan o'tmaydi
	}
	if forced == "" && in.Photo != nil {
		forced = "rasm — faqat qoralama" // rasmdagi yozuv matn filtridan o'tmaydi
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

	if in.Voice != nil && audio == nil {
		if audio = p.loadVoice(ctx, in.Voice); audio == nil {
			return
		}
	}
	var image *llm.Media
	if in.Photo != nil {
		pctx, pcancel := context.WithTimeout(ctx, 30*time.Second)
		data, err := in.Photo.Load(pctx)
		pcancel()
		if err != nil || len(data) == 0 {
			p.Store.Audit(ctx, p.Owner, "photo_error", "")
			p.Log.Warn("photo", "owner", p.Owner, "err", err)
			return
		}
		image = &llm.Media{MIME: "image/jpeg", Data: data}
	}

	limit := defaultHistory
	if p.HistoryLimit != nil {
		if n := p.HistoryLimit(ctx, p.Owner); n > 0 {
			limit = n
		}
	}
	hist, err := tg.History(ctx, in.ChatID, limit)
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
		History: hist, Incoming: in.Text, Away: st.CurrentAway(p.Now()), Audio: audio, Image: image,
		Knowledge: st.Knowledge, ContactNote: st.ContactNotes[in.ChatID],
	}
	var resp llm.Response
	connected := pro && p.Google != nil && p.Google.Connected(ctx, p.Owner) // Google — Pro funksiyasi
	if asst, ok := p.LLM.(llm.Assistant); ok && p.Propose != nil && !st.NoSuggest && (connected || p.CalendarFallback) {
		req.Assist = p.assistContext(lctx, &st, in, connected)
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

	if st.ShadowActive(now) {
		// Soya rejimi: hech narsa yuborilmaydi va bildirilmaydi — egasi o'zi yozgach solishtiriladi.
		p.rememberShadow(in, reply)
		p.Store.Audit(ctx, p.Owner, "shadow_prepared", fmt.Sprintf("chat=%d", in.ChatID))
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
	p.markSent(ctx, in.ChatID, reply)
	p.trackPromise(ctx, in.ChatID, reply)
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
	p.markSent(ctx, d.ChatID, d.Reply)
	p.learnFromEdit(ctx, d)
	p.trackPromise(ctx, d.ChatID, d.Reply)
	return nil
}

func (p *Pipeline) makeDraft(ctx context.Context, in Incoming, reply, reason string) {
	p.init() // manager ham chaqiradi (tug'ilgan kun tabrigi)
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
	p.Store.Audit(ctx, p.Owner, "draft_created", fmt.Sprintf("id=%d chat=%d", id, in.ChatID))
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

// notifyOnce egasiga xabar yuboradi, agar shu kalit bo'yicha every ichida yuborilmagan bo'lsa.
func (p *Pipeline) notifyOnce(ctx context.Context, key string, every time.Duration, text string, openApp bool) bool {
	if p.Notify == nil {
		return false
	}
	p.mu.Lock()
	last := p.notifiedAt[key]
	now := p.Now()
	if now.Sub(last) < every {
		p.mu.Unlock()
		return false
	}
	if len(p.notifiedAt) > 1000 { // chat kalitlari (urgent:<id>) cheksiz o'smasin
		for k, t := range p.notifiedAt {
			if now.Sub(t) > 24*time.Hour {
				delete(p.notifiedAt, k)
			}
		}
	}
	p.notifiedAt[key] = now
	p.mu.Unlock()
	p.Notify(ctx, p.Owner, text, openApp)
	return true
}

// assistContext Google'dan vaqt, band vaqtlar va kontakt eslatmasini yig'adi. Google
// xatolari javobni to'xtatmaydi — kontekstsiz davom etamiz.
func (p *Pipeline) assistContext(ctx context.Context, st *config.Settings, in Incoming, connected bool) *llm.Assist {
	loc := st.Location()
	now := p.Now().In(loc)
	a := &llm.Assist{Now: now, Loc: loc, Google: connected}
	if !connected {
		return a
	}
	ctx, cancel := context.WithTimeout(ctx, googleTimeout)
	defer cancel()
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
	if resp.Task != nil && (!a.Google || !titleOK(resp.Task.Title)) {
		resp.Task = nil // vazifalar faqat Google Tasks'ga yoziladi
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
