package userbot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/google"
	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
	"github.com/ozodjongg/secretar_ai_agent/internal/miniapp"
)

var (
	// ErrNeedPro — funksiya Pro tarifda.
	ErrNeedPro = errors.New("bu funksiya Pro tarifda")
	// ErrBusy — uslub allaqachon o'rganilmoqda.
	ErrBusy = errors.New("uslub o'rganilmoqda, biroz kuting")
	// ErrNoContact — savolda kontakt ismi topilmadi.
	ErrNoContact = errors.New("kontakt topilmadi")
	// ErrNoAnalyst — model egasi uchun topshiriqlarni qo'llamaydi.
	ErrNoAnalyst = errors.New("model bu funksiyani qo'llamaydi")
)

const (
	// learnChats — uslub o'rganishda o'qiladigan chatlar (eng faollari).
	learnChats = 15
	// learnHistory — har chatdan o'qiladigan xabarlar.
	learnHistory = 60
	// minSamples — kunlik yangilash uchun kamida shuncha juftlik kerak.
	minSamples = 3
	// refineHour — uslub profili yangilanadigan soat (egasining vaqti, tunda).
	refineHour = 3
)

// live — egasining jonli Telegram sessiyasi (bo'lmasa nil).
func (m *Manager) live(owner int64) (Telegram, *Pipeline) {
	m.mu.Lock()
	w := m.workers[owner]
	m.mu.Unlock()
	if w == nil {
		return nil, nil
	}
	return w.telegram(), w.pipe
}

func (m *Manager) pro(ctx context.Context, owner int64) bool {
	return m.Pro == nil || m.Pro(ctx, owner)
}

// StartLearnStyle egasining oxirgi yozishmalaridan uslub profilini fon rejimida yaratadi.
// Natija (yoki xato) bot orqali bildiriladi. Darhol tekshiriladigan xatolar qaytariladi.
func (m *Manager) StartLearnStyle(ctx context.Context, owner int64) error {
	m.init(ctx)
	an, ok := analystOf(m.LLM)
	if !ok {
		return ErrNoAnalyst
	}
	if !m.pro(ctx, owner) {
		return ErrNeedPro
	}
	if tg, _ := m.live(owner); tg == nil {
		return ErrNoLiveSession
	}
	m.mu.Lock()
	if m.learning[owner] {
		m.mu.Unlock()
		return ErrBusy
	}
	m.learning[owner] = true
	m.mu.Unlock()
	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.learning, owner)
			m.mu.Unlock()
		}()
		bctx, cancel := context.WithTimeout(context.WithoutCancel(m.BaseCtx), 3*time.Minute)
		defer cancel()
		err := m.learnStyle(bctx, an, owner)
		if m.Notify == nil {
			return
		}
		if err != nil {
			m.Log.Warn("learn style", "owner", owner, "err", err)
			m.Notify(bctx, owner, "Uslubingizni o'rganib bo'lmadi: yozishmalar yetarli emas yoki xatolik. Keyinroq qayta urinib ko'ring.", true)
			return
		}
		m.Notify(bctx, owner, "🪞 Uslubingiz o'rganildi. Ilovada «O'zimdek» uslubini tanlang — kotib xuddi sizdek yozadi. "+
			"Profilni ko'rib, xohlasangiz tahrirlang.", true)
	}()
	return nil
}

func (m *Manager) learnStyle(ctx context.Context, an llm.Analyst, owner int64) error {
	tg, _ := m.live(owner)
	if tg == nil {
		return ErrNoLiveSession
	}
	chats, err := tg.RecentChats(ctx, learnChats)
	if err != nil {
		return err
	}
	st, err := m.Store.GetSettings(ctx, owner)
	if err != nil {
		return err
	}
	var logs []llm.ChatLog
	for _, c := range chats {
		if !m.chatAllowed(&st, c.ID) {
			continue
		}
		hist, err := tg.History(ctx, c.ID, learnHistory)
		if err != nil {
			continue
		}
		mine := 0
		for _, t := range hist {
			if t.FromOwner {
				mine++
			}
		}
		if mine >= 2 {
			logs = append(logs, llm.ChatLog{Name: c.Name, Turns: hist})
		}
	}
	if len(logs) == 0 {
		return errors.New("egasining yozishmalari topilmadi")
	}
	res, err := an.LearnStyle(ctx, llm.StyleRequest{OwnerName: st.OwnerName, Current: st.StyleProfile, Chats: logs})
	if err != nil {
		return err
	}
	return m.saveProfile(ctx, owner, res, "learned")
}

// chatAllowed — egasi kotibni bu chatdan qoida bilan chetlatmagan (o'chirilgan qoida yo'q).
func (m *Manager) chatAllowed(st *config.Settings, chatID int64) bool {
	d := st.ResolveIgnoringQuiet(config.Contact{UserID: chatID, IsContact: true})
	return d.Reply || d.Reason == config.ReasonOff
}

// saveProfile profilni sozlamaga yozadi (o'sha paytdagi eng yangi sozlama ustiga).
func (m *Manager) saveProfile(ctx context.Context, owner int64, res llm.StyleResult, how string) error {
	st, err := m.Store.GetSettings(ctx, owner)
	if err != nil {
		return err
	}
	now := time.Now().Truncate(time.Second)
	st.StyleProfile = clip(res.Profile, config.MaxStyleProfile)
	st.StyleUpdatedAt = &now
	if res.Score >= 0 {
		st.StyleScore = res.Score
	}
	if err := m.Store.SaveSettings(ctx, owner, st); err != nil {
		return err
	}
	detail := "how=" + how
	if res.Score >= 0 {
		detail += fmt.Sprintf(" score=%d", res.Score)
	}
	m.Store.Audit(ctx, owner, "style_learned", detail)
	return nil
}

// refine to'plangan juftliklar asosida profilni yangilaydi va ishlatilganlarini o'chiradi.
// Juftliklar yetarli bo'lmasa (false, nil).
func (m *Manager) refine(ctx context.Context, an llm.Analyst, owner int64, st *config.Settings, min int) (bool, error) {
	samples, err := m.Store.ListStyleSamples(ctx, owner, 50)
	if err != nil || len(samples) < min {
		return false, err
	}
	req := llm.StyleRequest{OwnerName: st.OwnerName, Current: st.StyleProfile}
	for _, s := range samples {
		req.Samples = append(req.Samples, llm.StyleSample{Incoming: s.Incoming, Predicted: s.Predicted, Actual: s.Actual})
	}
	res, err := an.LearnStyle(ctx, req)
	if err != nil {
		return false, err
	}
	if err := m.saveProfile(ctx, owner, res, "refined"); err != nil {
		return false, err
	}
	return true, m.Store.DeleteStyleSamples(ctx, owner, samples[len(samples)-1].ID)
}

// RefineStyles — o'qitish (yoki soya) rejimidagi egalarning uslub profilini kuniga bir marta,
// tunda (soat 3 da) to'plangan juftliklar asosida yangilaydi.
func (m *Manager) RefineStyles(ctx context.Context, now time.Time) {
	m.init(ctx)
	an, ok := analystOf(m.LLM)
	if !ok {
		return
	}
	owners, err := m.Store.ListActiveOwners(ctx)
	if err != nil {
		return
	}
	for _, owner := range owners {
		st, err := m.Store.GetSettings(ctx, owner)
		if err != nil || !(st.Learn || st.ShadowActive(now)) {
			continue
		}
		local := now.In(st.Location())
		if local.Hour() != refineHour || !m.pro(ctx, owner) || (m.Entitled != nil && !m.Entitled(ctx, owner)) {
			continue
		}
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
		if n, err := m.Store.CountAudit(ctx, owner, []string{"style_refine_run"}, day); err != nil || n > 0 {
			continue
		}
		m.Store.Audit(ctx, owner, "style_refine_run", "")
		if _, err := m.refine(ctx, an, owner, &st, minSamples); err != nil {
			m.Log.Warn("refine style", "owner", owner, "err", err)
		}
	}
}

// Soya rejimi natijasi tugmalari (botcmd.ShadowCallback).
const (
	ShadowAuto  = "shd:auto"
	ShadowDraft = "shd:draft"
)

// SendShadowReports — soya rejimi tugagan egalarga natijani bir marta yuboradi: nechta javob
// jim tayyorlandi, egasinikiga qanchalik mos keldi va avto rejimni yoqish taklifi.
func (m *Manager) SendShadowReports(ctx context.Context, now time.Time) {
	m.init(ctx)
	if m.Notify == nil && m.Offer == nil {
		return
	}
	owners, err := m.Store.ListActiveOwners(ctx)
	if err != nil {
		return
	}
	for _, owner := range owners {
		st, err := m.Store.GetSettings(ctx, owner)
		if err != nil || st.ShadowUntil == nil || now.Before(*st.ShadowUntil) || now.Sub(*st.ShadowUntil) > 7*24*time.Hour {
			continue
		}
		if n, err := m.Store.CountAudit(ctx, owner, []string{"shadow_report"}, *st.ShadowUntil); err != nil || n > 0 {
			continue
		}
		m.Store.Audit(ctx, owner, "shadow_report", "")
		if an, ok := analystOf(m.LLM); ok {
			if _, err := m.refine(ctx, an, owner, &st, 1); err != nil {
				m.Log.Warn("shadow refine", "owner", owner, "err", err)
			}
			st, _ = m.Store.GetSettings(ctx, owner)
		}
		prepared, _ := m.Store.CountAudit(ctx, owner, []string{"shadow_prepared"}, st.ShadowUntil.AddDate(0, 0, -config.MaxShadowDays))
		text, buttons := shadowReport(owner, prepared, st.StyleScore, st.Mode)
		if m.Offer != nil && len(buttons) > 0 {
			m.Offer(ctx, owner, text, buttons)
		} else if m.Notify != nil {
			m.Notify(ctx, owner, text, true)
		}
	}
}

func shadowReport(owner int64, prepared, score int, mode config.Mode) (string, [][2]string) {
	var b strings.Builder
	b.WriteString("🪞 Soya rejimi tugadi\n\n")
	fmt.Fprintf(&b, "Kotib %d ta javobni jim tayyorladi va sizning javoblaringiz bilan solishtirdi.\n", prepared)
	if score > 0 {
		fmt.Fprintf(&b, "O'rtacha moslik: %d%%.\n", score)
	}
	b.WriteString("Uslub profilingiz yangilandi — «O'zimdek» uslubi endi sizga yanada yaqin.")
	if mode == config.ModeAuto {
		return b.String(), nil
	}
	switch {
	case score >= 75:
		b.WriteString("\n\nKotib sizdek yozyapti. Avto rejimni yoqaymi? (Sezgir mavzular baribir faqat qoralama bo'ladi.)")
	case score > 0:
		b.WriteString("\n\nHozircha qoralama rejimida qolish tavsiya etiladi: tahrirlaringizdan o'rganishda davom etadi.")
	}
	// Tugmada akkaunt ID'si: qo'shimcha akkaunt hisoboti asosiy akkaunt chatiga keladi.
	return b.String(), [][2]string{
		{"⚡️ Avto rejim", fmt.Sprintf("%s:%d", ShadowAuto, owner)},
		{"✍️ Qoralamada qolish", fmt.Sprintf("%s:%d", ShadowDraft, owner)},
	}
}

// ApplyShadowChoice — egasi soya rejimi natijasidan keyin tanlagan rejim (bot tugmasi).
func (m *Manager) ApplyShadowChoice(ctx context.Context, owner int64, data string) (string, error) {
	st, err := m.Store.GetSettings(ctx, owner)
	if err != nil {
		return "", err
	}
	switch data {
	case ShadowAuto:
		st.Mode = config.ModeAuto
		if strings.TrimSpace(st.StyleProfile) != "" {
			st.DefaultPersona = config.PersonaMine
		}
	case ShadowDraft:
		if st.Mode == config.ModeOff {
			st.Mode = config.ModeDraft
		}
		if strings.TrimSpace(st.StyleProfile) != "" {
			st.DefaultPersona = config.PersonaMine
		}
		st.Learn = true
	default:
		return "", errors.New("noma'lum tanlov")
	}
	if err := m.Store.SaveSettings(ctx, owner, st); err != nil {
		return "", err
	}
	m.Store.Audit(ctx, owner, "settings_saved", "mode="+string(st.Mode))
	if data == ShadowAuto {
		return "⚡️ Avto rejim yoqildi, «O'zimdek» uslubi bilan.", nil
	}
	return "✍️ Qoralama rejimi, «O'zimdek» uslubi; o'qitish davom etadi.", nil
}

// ---- /sor: yozishma haqida savol ----

const askHistory = 100

// Ask egasining savolidagi kontaktni topadi va o'sha chat yozishmasidan javob beradi (Pro).
func (m *Manager) Ask(ctx context.Context, owner int64, question string) (string, error) {
	an, ok := analystOf(m.LLM)
	if !ok {
		return "", ErrNoAnalyst
	}
	if !m.pro(ctx, owner) {
		return "", ErrNeedPro
	}
	tg, _ := m.live(owner)
	if tg == nil {
		return "", ErrNoLiveSession
	}
	var cands []miniapp.ContactInfo
	if rc, err := tg.RecentChats(ctx, 50); err == nil {
		cands = append(cands, rc...) // yaqinda yozishganlar birinchi
	}
	if cs, err := tg.Contacts(ctx); err == nil {
		cands = append(cands, cs...)
	}
	c, ok := matchContact(question, cands)
	if !ok {
		return "", ErrNoContact
	}
	st, err := m.Store.GetSettings(ctx, owner)
	if err != nil {
		return "", err
	}
	if !m.chatAllowed(&st, c.ID) {
		return "", fmt.Errorf("%w: %s bilan yozishma qoidalaringiz bo'yicha kotibdan yopiq", ErrNoContact, c.Name)
	}
	hist, err := tg.History(ctx, c.ID, askHistory)
	if err != nil {
		return "", err
	}
	if len(hist) == 0 {
		return c.Name + " bilan yozishma topilmadi.", nil
	}
	ans, err := an.Answer(ctx, llm.AnswerRequest{OwnerName: st.OwnerName, Question: question, Now: time.Now().In(st.Location()),
		Chat: llm.ChatLog{Name: c.Name, Turns: hist}})
	if err != nil {
		return "", err
	}
	m.Store.Audit(ctx, owner, "ask_answered", "")
	return "💬 " + c.Name + "\n\n" + ans, nil
}

// words — matnni kichik harfli so'zlarga ajratadi (apostroflar so'z ichida qoladi).
func words(s string) []string {
	s = normalizeText(s)
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' })
}

// maxSuffix — ismdan keyingi qo'shimcha uzunligi chegarasi ("Azizga", "Azizning"), shunda
// "Azizbekni" Aziz bilan adashmaydi.
const maxSuffix = 4

// matchContact savoldagi kontaktni topadi. Ism so'zlari savoldagi so'zlarning boshida kelishi
// kerak (o'zbekcha qo'shimchalar: "Azizga", "Aziz bilan"). Eng ko'p so'zi mos kelgan kontakt
// tanlanadi; teng bo'lsa — ro'yxatda oldinroq (yaqinda yozishgan) turgani.
func matchContact(question string, cands []miniapp.ContactInfo) (miniapp.ContactInfo, bool) {
	q := words(question)
	best, bestScore := miniapp.ContactInfo{}, 0
	for _, c := range cands {
		score := 0
		for _, nw := range words(c.Name) {
			if len([]rune(nw)) < 3 {
				continue
			}
			for _, qw := range q {
				if strings.HasPrefix(qw, nw) && len([]rune(qw))-len([]rune(nw)) <= maxSuffix {
					score++
					break
				}
			}
		}
		if score > bestScore {
			best, bestScore = c, score
		}
	}
	return best, bestScore > 0
}

// ---- Tug'ilgan kunlar ----

// birthdayHour — tabrik takliflari yuboriladigan soat.
const birthdayHour = 9

// Birthday — Google Contacts'dagi bugun tug'ilgan kishi.
type Birthday = google.Birthday

// Birthdays — bugungi tug'ilgan kunlar (google.Client); Google interfeysidan alohida, ixtiyoriy.
type Birthdays interface {
	Birthdays(ctx context.Context, owner int64, month time.Month, day int) ([]Birthday, error)
}

// SendBirthdays — Google ulangan Pro egalariga soat 9:00 da bugun tug'ilganlar uchun tabrik
// qoralamasini taklif qiladi. Hech narsa avtomatik yuborilmaydi: qoralamani egasi tasdiqlaydi.
func (m *Manager) SendBirthdays(ctx context.Context, now time.Time) {
	m.init(ctx)
	bd, ok := m.Google.(Birthdays)
	if !ok || m.Google == nil {
		return
	}
	owners, err := m.Store.ListActiveOwners(ctx)
	if err != nil {
		return
	}
	for _, owner := range owners {
		st, err := m.Store.GetSettings(ctx, owner)
		if err != nil || st.NoBirthdays || st.Mode == config.ModeOff {
			continue
		}
		local := now.In(st.Location())
		if local.Hour() != birthdayHour || !m.pro(ctx, owner) || (m.Entitled != nil && !m.Entitled(ctx, owner)) {
			continue
		}
		if !m.Google.Connected(ctx, owner) {
			continue
		}
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
		if n, err := m.Store.CountAudit(ctx, owner, []string{"birthdays_checked"}, day); err != nil || n > 0 {
			continue
		}
		m.Store.Audit(ctx, owner, "birthdays_checked", "")
		people, err := bd.Birthdays(ctx, owner, local.Month(), local.Day())
		if err != nil {
			m.Log.Warn("birthdays", "owner", owner, "err", err)
			continue
		}
		for i, b := range people {
			if i >= 10 {
				break
			}
			m.birthday(ctx, owner, &st, b)
		}
	}
}

func (m *Manager) birthday(ctx context.Context, owner int64, st *config.Settings, b Birthday) {
	tg, pipe := m.live(owner)
	var c miniapp.ContactInfo
	found := false
	if tg != nil {
		if cs, err := tg.Contacts(ctx); err == nil {
			c, found = matchPerson(b, cs)
		}
	}
	an, ok := analystOf(m.LLM)
	if !found || !ok || pipe == nil || !m.chatAllowed(st, c.ID) {
		if m.Notify != nil {
			m.Notify(ctx, owner, "🎂 Bugun "+b.Name+"ning tug'ilgan kuni. Tabriklashni unutmang!", false)
		}
		return
	}
	hist, _ := tg.History(ctx, c.ID, 20)
	persona := st.ResolveIgnoringQuiet(config.Contact{UserID: c.ID, Name: c.Name, IsContact: true}).Persona
	if persona == "" {
		persona = config.Presets["neutral"]
	}
	text, err := an.Greeting(ctx, llm.GreetingRequest{OwnerName: st.OwnerName, Persona: persona, Language: st.Language, Name: c.Name, History: hist})
	if err != nil {
		m.Log.Warn("greeting", "owner", owner, "err", err)
		if m.Notify != nil {
			m.Notify(ctx, owner, "🎂 Bugun "+c.Name+"ning tug'ilgan kuni. Tabriklashni unutmang!", false)
		}
		return
	}
	m.Store.Audit(ctx, owner, "birthday_draft", "")
	pipe.makeDraft(ctx, Incoming{ChatID: c.ID, Text: "🎂 Bugun " + c.Name + "ning tug'ilgan kuni", Contact: config.Contact{UserID: c.ID, Name: c.Name}},
		text, "tug'ilgan kun tabrigi")
}

// matchPerson Google kontaktini Telegram kontaktiga moslaydi: telefon (oxirgi 9 raqam), bo'lmasa to'liq ism.
func matchPerson(b Birthday, cs []miniapp.ContactInfo) (miniapp.ContactInfo, bool) {
	tail := func(p string) string {
		if len(p) > 9 {
			return p[len(p)-9:]
		}
		return p
	}
	if len(b.Phone) >= 7 {
		for _, c := range cs {
			if c.Phone != "" && tail(c.Phone) == tail(b.Phone) {
				return c, true
			}
		}
	}
	name := strings.Join(words(b.Name), " ")
	for _, c := range cs {
		if name != "" && strings.Join(words(c.Name), " ") == name {
			return c, true
		}
	}
	return miniapp.ContactInfo{}, false
}
