// Package guard: kiruvchi xabarni tasniflash va model javobini yuborishdan
// oldin tekshirish. Maqsad — xavfli mavzularda AI avtomatik javob bermasligi
// (mijozning tasdig'iga yuboriladi) va prompt injection'dan himoya.
package guard

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Verdict — qaror: avtomatik javob berish mumkinmi.
type Verdict int

const (
	Allow   Verdict = iota // AI javob yozishi va (rejimga qarab) yuborishi mumkin
	Confirm                // AI draft yozadi, lekin yuborishdan oldin mijoz tasdiqlaydi
	Block                  // umuman javob yozilmaydi (mijozga xabar beriladi)
)

type Result struct {
	Verdict Verdict
	Reason  string
}

// Sezgir mavzular (o'zbek lotin/kirill, rus, ingliz). Bu — birinchi qatlam;
// aniqroq tasniflash uchun keyinchalik model-asosli klassifikator qo'shish mumkin.
var sensitive = []struct {
	reason string
	re     *regexp.Regexp
}{
	// Go regexp'da \b faqat ASCII uchun ishlaydi, shuning uchun kirillcha so'zlar
	// va o'zbekcha qo'shimchali shakllar (o'tkazib, qarzga...) o'zak (stem) bilan tekshiriladi.
	{"moliya", regexp.MustCompile(`(?i)(\bpul(ni|ga|ing|im)?\b|qarz|o'tkaz|o‘tkaz|perevod|перевод|деньг|долг|займ|одолж|карта\s*raqam|номер\s*карты|\bcvv\b|\bcvc\b|\biban\b|\bswift\b|\bloan\b|\bwire\s+transfer)`)},
	{"kod/parol", regexp.MustCompile(`(?i)(parol|пароль|password|\botp\b|sms\s*kod|код\s*из\s*смс|tasdiqlash\s*kodi|kod\s*yubor|verification\s*code|login\s*code|2fa|seed\s*phrase|private\s*key)`)},
	{"hujjat", regexp.MustCompile(`(?i)(pasport|паспорт|passport|jshshir|pinfl|\bstir\b|tug'ilganlik\s*guvohnoma)`)},
	{"manzil/joylashuv", regexp.MustCompile(`(?i)(qayerdasan\s*hozir|manzilingni\s*yubor|lokatsiya|геолокац|where\s*do\s*you\s*live|send\s*(me\s*)?your\s*location)`)},
	{"huquqiy/tibbiy", regexp.MustCompile(`(?i)(\bsud(ga|da|dan)?\b|prokuratura|shartnoma|договор|\bсуд(е|а|у)?(\s|$|[.,!?])|лекарств|диагноз|retsept)`)},
	// Kontakt jiddiy so'rasa — mijozning o'zi javob bersin (AI inson ekanini tasdiqlamasligi uchun).
	{"kimligi-so'rov", regexp.MustCompile(`(?i)(bot\s*misan|robot\s*misan|sun'iy\s*intellekt\s*misan|haqiqiy\s*odam\s*misan|real\s*odam\s*misan|ты\s+бот|ты\s+робот|ты\s+нейросеть|are\s+you\s+(a\s+)?(bot|robot|real|human|ai))`)},
	{"shaxsiy-his", regexp.MustCompile(`(?i)(o'zimni\s*o'ldir|o‘zimni\s*o‘ldir|yashagim\s*kelmayapti|хочу\s*умереть|suicide|kill\s*myself)`)},
}

// Prompt injection belgilari: kontakt xabarida modelga "buyruq berishga" urinish.
var injection = regexp.MustCompile(`(?i)(ignore\s+(all\s+)?(previous|above|prior)|system\s*prompt|you\s+are\s+now|forget\s+(your\s+)?instructions|oldingi\s+ko'rsatma|oldingi\s+ko‘rsatma|yuqoridagi\s+(ko'rsatma|buyruq)|игнорируй\s+(все\s+)?(предыдущ|инструкц)|системн\w+\s+промпт|admin\s*mode|developer\s*mode|jailbreak)`)

// ClassifyIncoming kiruvchi xabarni baholaydi.
func ClassifyIncoming(text string) Result {
	t := strings.TrimSpace(text)
	if t == "" {
		return Result{Allow, ""}
	}
	if injection.MatchString(t) {
		// Injection — modelga umuman uzatmaymiz: kontakt "buyruq" berayapti.
		return Result{Block, "prompt-injection"}
	}
	for _, s := range sensitive {
		if s.re.MatchString(t) {
			if s.reason == "shaxsiy-his" {
				return Result{Block, s.reason} // avtomatik emas — mijoz o'zi javob bersin
			}
			return Result{Confirm, s.reason}
		}
	}
	return Result{Allow, ""}
}

// Wrap kiruvchi matnni model uchun "ma'lumot" sifatida o'raydi. Ichidagi
// delimiter'larni zararsizlantiradi, shunda kontakt blokdan "chiqib ketolmaydi".
func Wrap(text string) string {
	text = strings.ReplaceAll(text, "<<<", "‹‹‹")
	text = strings.ReplaceAll(text, ">>>", "›››")
	if utf8.RuneCountInString(text) > 2000 {
		r := []rune(text)
		text = string(r[:2000]) + "…"
	}
	return "<<<KONTAKT_XABARI\n" + text + "\nKONTAKT_XABARI>>>"
}

// Chiqish tekshiruvi: model javobi yuborishga yaroqlimi.
var (
	urlRe     = regexp.MustCompile(`(?i)(https?://|www\.|t\.me/|@[a-z0-9_]{5,})`)
	cardRe    = regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`)
	phoneRe   = regexp.MustCompile(`(?:\+|\b)\d(?:[\s\-()]*\d){8,}\b`)
	leakRe    = regexp.MustCompile(`(?i)(system\s*prompt|KONTAKT_XABARI|men\s+(bir\s+)?(AI|sun'iy\s*intellekt|bot|til\s*modeli)|as\s+an\s+AI|language\s+model)`)
	maxReplyR = 700
)

// CheckOutput model javobini tekshiradi. ok=false bo'lsa — yuborilmaydi, draft'ga tushadi.
// allowLinks=false bo'lsa, javobda havola/@username bo'lmasligi shart (spam-pattern xavfi).
func CheckOutput(reply string, allowLinks bool) (ok bool, reason string) {
	r := strings.TrimSpace(reply)
	switch {
	case r == "":
		return false, "bo'sh javob"
	case utf8.RuneCountInString(r) > maxReplyR:
		return false, "juda uzun"
	case leakRe.MatchString(r):
		return false, "AI/system-prompt belgisi sizib chiqdi"
	case cardRe.MatchString(r):
		return false, "karta/uzun raqam"
	case phoneRe.MatchString(r):
		return false, "telefon raqam"
	case !allowLinks && urlRe.MatchString(r):
		return false, "havola/username"
	}
	return true, ""
}
