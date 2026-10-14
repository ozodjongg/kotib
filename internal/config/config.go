// Package config: mijozning sozlamalari (Mini App'da tahrirlanadi) va
// kontaktga qarab qaror chiqarish (javob beraymi, qaysi rejim, qaysi persona).
package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type Mode string

const (
	ModeOff   Mode = "off"   // javob yozilmaydi
	ModeDraft Mode = "draft" // AI draft yozadi, mijoz tasdiqlaydi (default)
	ModeAuto  Mode = "auto"  // AI o'zi yuboradi
)

func (m Mode) Valid() bool { return m == ModeOff || m == ModeDraft || m == ModeAuto }

// Match — qoida qaysi kontaktlarga tegishli. Bo'sh maydonlar "e'tiborga olinmaydi";
// barcha to'ldirilgan shartlar bir vaqtda bajarilishi kerak (AND).
type Match struct {
	UserIDs   []int64  `json:"user_ids,omitempty"`   // aniq kontaktlar
	Tags      []string `json:"tags,omitempty"`       // mijoz o'zi bergan teglar ("do'st", "oila"...)
	IsContact *bool    `json:"is_contact,omitempty"` // true: telefon kitobida bor; false: yo'q
}

// Rule — bitta qoida. Ro'yxatda birinchi mos kelgani ishlaydi.
type Rule struct {
	ID       string `json:"id"`
	Title    string `json:"title,omitempty"`
	Match    Match  `json:"match"`
	Mode     *Mode  `json:"mode,omitempty"`    // nil → umumiy Settings.Mode
	Persona  string `json:"persona,omitempty"` // preset kaliti yoki "custom"
	Custom   string `json:"custom,omitempty"`  // persona == "custom" bo'lsa matn
	Disabled bool   `json:"disabled,omitempty"`
}

type QuietHours struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start"` // "23:00"
	End     string `json:"end"`   // "07:00"
	TZ      string `json:"tz"`    // "Asia/Tashkent"
}

// Settings — bitta mijozning to'liq sozlamasi.
type Settings struct {
	Mode         Mode `json:"mode"`
	ReplyUnknown bool `json:"reply_unknown"` // kontaktda yo'qlarga javob (default: false)
	ReplyGroups  bool `json:"reply_groups"`  // guruhlarda javob (default: false, tavsiya etilmaydi)
	// ReplyBots — botlarga ham javob tayyorlash (default: false). Yoqilsa ham faqat
	// qoralama: bot bilan avtomatik yozishma cheksiz aylanishga (spam, ban) olib keladi.
	ReplyBots  bool `json:"reply_bots"`
	AllowLinks bool `json:"allow_links"` // javobda havola (default: false)
	// DraftsInBot — qoralama matni bot chatiga tugmalar bilan yuboriladi (tasdiqlash ilovasiz).
	// Default o'chiq: yoqilsa kontakt xabari va javob matni bot chati tarixida qoladi.
	DraftsInBot bool `json:"drafts_in_bot"`
	// Away — egasining hozirgi holati ("rulda", "yig'ilishda"); kotib javobda hisobga oladi.
	// Odatda avtomatlashtirish havolasi (Focus / MacroDroid) o'rnatadi; bo'sh — yo'q.
	Away           string     `json:"away,omitempty"`
	DefaultPersona string     `json:"default_persona"` // preset kaliti
	DefaultCustom  string     `json:"default_custom,omitempty"`
	OwnerName      string     `json:"owner_name"` // mijoz ismi — "men X man" kontekst uchun
	Language       string     `json:"language"`   // "uz", "ru", "auto"
	Quiet          QuietHours `json:"quiet_hours"`
	Rules          []Rule     `json:"rules"`
	// ContactTags: userID → teglar. Mini App kontaktlarga teg beradi.
	ContactTags map[int64][]string `json:"contact_tags,omitempty"`
}

// MaxAway — Settings.Away uzunligi chegarasi.
const MaxAway = 100

// Default xavfsiz boshlang'ich sozlama.
func Default() Settings {
	return Settings{
		Mode:           ModeDraft,
		ReplyUnknown:   false,
		ReplyGroups:    false,
		ReplyBots:      false,
		AllowLinks:     false,
		DefaultPersona: "neutral",
		Language:       "auto",
		Quiet:          QuietHours{Enabled: true, Start: "23:00", End: "07:00", TZ: "Asia/Tashkent"},
		Rules:          []Rule{},
	}
}

// Presets — tayyor personalar. Matn modelning system prompt'iga qo'shiladi.
var Presets = map[string]string{
	"neutral": "Do'stona, muloyim va qisqa yoz. Ortiqcha rasmiyatchiliksiz.",
	"joke":    "Hazil ruhida, yengil va samimiy yoz; ba'zan mayin hazil qil, lekin haqorat va qo'pol so'zlarsiz.",
	"son":     "Egasining o'g'li nomidan yoz: hurmat bilan, mehr bilan, oddiy so'zlarda. Ota-onaga 'siz' deb murojaat qil.",
	"formal":  "Rasmiy va aniq yoz: 'siz' deb murojaat qil, qisqa, ishbilarmon uslub.",
	"brief":   "Juda qisqa javob ber: bitta-ikkita jumla.",
}

const (
	maxRules      = 50
	maxCustomLen  = 600
	maxTagsPerKey = 20
	maxTagLen     = 32
	maxIDsPerRule = 200
	maxTitleLen   = 100
	maxRuleIDLen  = 64
	maxTaggedIDs  = 5000
)

// Validate Mini App'dan kelgan (ishonchsiz) sozlamani tekshiradi va normallashtiradi.
func (s *Settings) Validate() error {
	if !s.Mode.Valid() {
		return fmt.Errorf("noto'g'ri mode: %q", s.Mode)
	}
	if _, ok := Presets[s.DefaultPersona]; !ok && s.DefaultPersona != "custom" {
		return fmt.Errorf("noma'lum default_persona: %q", s.DefaultPersona)
	}
	if utf8.RuneCountInString(s.DefaultCustom) > maxCustomLen {
		return errors.New("default_custom juda uzun")
	}
	if utf8.RuneCountInString(s.OwnerName) > 64 {
		return errors.New("owner_name juda uzun")
	}
	if utf8.RuneCountInString(s.Away) > MaxAway || strings.ContainsAny(s.Away, "\r\n") {
		return errors.New("away juda uzun yoki ko'p qatorli")
	}
	switch s.Language {
	case "uz", "ru", "en", "auto":
	default:
		return fmt.Errorf("noma'lum til: %q", s.Language)
	}
	if len(s.Quiet.Start) > 5 || len(s.Quiet.End) > 5 || len(s.Quiet.TZ) > 64 {
		return errors.New("quiet_hours noto'g'ri")
	}
	if s.Quiet.Enabled {
		if _, err := parseHM(s.Quiet.Start); err != nil {
			return fmt.Errorf("quiet.start: %w", err)
		}
		if _, err := parseHM(s.Quiet.End); err != nil {
			return fmt.Errorf("quiet.end: %w", err)
		}
		if _, err := time.LoadLocation(s.Quiet.TZ); err != nil {
			return fmt.Errorf("quiet.tz: %w", err)
		}
	}
	if len(s.Rules) > maxRules {
		return fmt.Errorf("qoidalar %d tadan oshmasligi kerak", maxRules)
	}
	seen := map[string]bool{}
	for i := range s.Rules {
		r := &s.Rules[i]
		if r.ID == "" || len(r.ID) > maxRuleIDLen || seen[r.ID] {
			return fmt.Errorf("qoida #%d: id bo'sh yoki takrorlangan", i)
		}
		seen[r.ID] = true
		if r.Mode != nil && !r.Mode.Valid() {
			return fmt.Errorf("qoida %s: noto'g'ri mode", r.ID)
		}
		if r.Persona != "" && r.Persona != "custom" {
			if _, ok := Presets[r.Persona]; !ok {
				return fmt.Errorf("qoida %s: noma'lum persona", r.ID)
			}
		}
		if utf8.RuneCountInString(r.Custom) > maxCustomLen {
			return fmt.Errorf("qoida %s: custom juda uzun", r.ID)
		}
		if utf8.RuneCountInString(r.Title) > maxTitleLen {
			return fmt.Errorf("qoida %s: nomi juda uzun", r.ID)
		}
		if len(r.Match.UserIDs) > maxIDsPerRule || len(r.Match.Tags) > maxTagsPerKey {
			return fmt.Errorf("qoida %s: match juda katta", r.ID)
		}
		for _, id := range r.Match.UserIDs {
			if id <= 0 {
				return fmt.Errorf("qoida %s: noto'g'ri user id", r.ID)
			}
		}
		for j, t := range r.Match.Tags {
			t = strings.ToLower(strings.TrimSpace(t))
			if t == "" || utf8.RuneCountInString(t) > maxTagLen {
				return fmt.Errorf("qoida %s: teg bo'sh yoki juda uzun", r.ID)
			}
			r.Match.Tags[j] = t
		}
	}
	if s.Rules == nil {
		s.Rules = []Rule{}
	}
	if len(s.ContactTags) > maxTaggedIDs {
		return errors.New("contact_tags juda ko'p")
	}
	for uid, tags := range s.ContactTags {
		if uid <= 0 || len(tags) > maxTagsPerKey {
			return errors.New("contact_tags noto'g'ri")
		}
		for j, t := range tags {
			t = strings.ToLower(strings.TrimSpace(t))
			if t == "" || utf8.RuneCountInString(t) > maxTagLen {
				return errors.New("teg bo'sh yoki juda uzun")
			}
			tags[j] = t
		}
	}
	return nil
}

func parseHM(v string) (int, error) {
	t, err := time.Parse("15:04", v)
	if err != nil {
		return 0, errors.New("format HH:MM bo'lishi kerak")
	}
	return t.Hour()*60 + t.Minute(), nil
}

// ---- Qaror ----

// Contact — kiruvchi xabar jo'natuvchisi haqida runtime ma'lumot.
type Contact struct {
	UserID    int64
	Name      string // Telegram'dagi ism (Google Contacts'da qidirish, tadbir nomi uchun)
	Phone     string // faqat raqamlar; Telegram faqat kontaktlar uchun beradi
	IsContact bool   // mijozning Telegram kontaktlarida bormi
	IsBot     bool
	IsGroup   bool // xabar guruhdan keldimi
	IsChannel bool
}

// IsTelegramService — Telegram'ning rasmiy xizmat akkauntlari (777000 — login kodlari,
// xavfsizlik ogohlantirishlari; 42777 — eski xizmat ID'si). Ularning xabarlari hech qachon
// qayta ishlanmaydi va AI'ga yuborilmaydi: ichida kirish kodi bo'lishi mumkin.
func IsTelegramService(userID int64) bool { return userID == 777000 || userID == 42777 }

// Decision — Resolve natijasi.
type Decision struct {
	Reply   bool
	Mode    Mode
	Persona string // tayyor prompt matni
	RuleID  string
	Reason  string // Reply=false bo'lsa sabab (log/audit uchun)
}

// Resolve kontakt uchun qaror chiqaradi. Tartib: qattiq to'siqlar → quiet hours → qoidalar.
func (s *Settings) Resolve(c Contact, now time.Time) Decision {
	no := func(why string) Decision { return Decision{Reply: false, Mode: ModeOff, Reason: why} }

	if s.Mode == ModeOff {
		return no("o'chirilgan")
	}
	if IsTelegramService(c.UserID) {
		return no("Telegram xizmati")
	}
	if c.IsBot && !s.ReplyBots {
		return no("bot")
	}
	if c.IsChannel {
		return no("kanal")
	}
	if c.IsGroup && !s.ReplyGroups {
		return no("guruh")
	}
	// Botlar kontaktlarda bo'lmaydi: ReplyBots yoqilgan bo'lsa, kontakt sharti ularga qo'llanmaydi.
	if !c.IsContact && !s.ReplyUnknown && !c.IsBot {
		return no("kontaktda yo'q")
	}
	if s.inQuiet(now) {
		return no("tinch soatlar")
	}

	d := Decision{Reply: true, Mode: s.Mode, Persona: s.personaText(s.DefaultPersona, s.DefaultCustom)}
	for i := range s.Rules {
		r := &s.Rules[i]
		if r.Disabled || !s.matches(r.Match, c) {
			continue
		}
		d.RuleID = r.ID
		if r.Mode != nil {
			d.Mode = *r.Mode
		}
		if r.Persona != "" {
			d.Persona = s.personaText(r.Persona, r.Custom)
		}
		break
	}
	if d.Mode == ModeOff {
		return Decision{Reply: false, Mode: ModeOff, RuleID: d.RuleID, Reason: "qoida: o'chirilgan"}
	}
	// Mijoz Settings.Mode=draft qilgan bo'lsa, qoida uni "auto"ga ko'tara olmaydi.
	if s.Mode == ModeDraft && d.Mode == ModeAuto {
		d.Mode = ModeDraft
	}
	// Botga hech qachon avtomatik javob yo'q (bot ↔ kotib cheksiz yozishmasi).
	if c.IsBot && d.Mode == ModeAuto {
		d.Mode = ModeDraft
	}
	return d
}

func (s *Settings) personaText(key, custom string) string {
	if key == "custom" {
		return strings.TrimSpace(custom)
	}
	if t, ok := Presets[key]; ok {
		return t
	}
	return Presets["neutral"]
}

func (s *Settings) matches(m Match, c Contact) bool {
	if len(m.UserIDs) == 0 && len(m.Tags) == 0 && m.IsContact == nil {
		return false // bo'sh match hech narsaga mos kelmaydi (tasodifan "hammaga" bo'lib qolmasin)
	}
	if len(m.UserIDs) > 0 && !containsID(m.UserIDs, c.UserID) {
		return false
	}
	if m.IsContact != nil && *m.IsContact != c.IsContact {
		return false
	}
	if len(m.Tags) > 0 {
		have := s.ContactTags[c.UserID]
		ok := false
		for _, want := range m.Tags {
			want = strings.ToLower(strings.TrimSpace(want))
			for _, h := range have {
				if h == want {
					ok = true
				}
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func containsID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// Location — mijozning vaqt zonasi (tinch soatlar sozlamasidan), default Asia/Tashkent.
func (s *Settings) Location() *time.Location {
	if loc, err := time.LoadLocation(s.Quiet.TZ); err == nil && s.Quiet.TZ != "" {
		return loc
	}
	if loc, err := time.LoadLocation("Asia/Tashkent"); err == nil {
		return loc
	}
	return time.FixedZone("Asia/Tashkent", 5*60*60)
}

func (s *Settings) inQuiet(now time.Time) bool {
	if !s.Quiet.Enabled {
		return false
	}
	loc, err := time.LoadLocation(s.Quiet.TZ)
	if err != nil {
		return false
	}
	start, err1 := parseHM(s.Quiet.Start)
	end, err2 := parseHM(s.Quiet.End)
	if err1 != nil || err2 != nil || start == end {
		return false
	}
	l := now.In(loc)
	cur := l.Hour()*60 + l.Minute()
	if start < end {
		return cur >= start && cur < end
	}
	return cur >= start || cur < end // yarim tundan o'tadigan oraliq (23:00–07:00)
}
