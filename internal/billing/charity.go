package billing

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// Hayriya: har obuna to'lovining CharityPercent foizi va Stars xayriyalari (to'liq) hayriyaga ajratiladi.
//
// Bot API'da Stars'ni boshqa akkauntga o'tkazish yo'q: Stars bot balansida yig'iladi, admin ularni
// Fragment orqali (21 kunlik ushlab turishdan keyin) yechib, tashkilotga o'tkazadi va isbotini kiritadi.
// Shuning uchun tizim hisobni yuritadi: oy tugagach o'tgan oy uchun summa hisoblanadi va adminga xabar boradi.
//
// Hisob yig'ma (cumulative): oy summasi = (boshlanishdan oy oxirigacha obunalar × foiz + xayriyalar) −
// avvalgi oylarga hisoblangan summa. Shunda kech kelgan refund keyingi oyda ayiriladi va yaxlitlashdan
// yo'qotish to'planib qolmaydi.

const (
	MaxCharityPercent = 100
	maxProof          = 300
	maxCharityNote    = 200
	charityHistory    = 24
)

var (
	ErrCharityDisabled = errors.New("hayriya yoqilmagan")
	ErrDonationAmount  = fmt.Errorf("summa 1–%d ⭐ bo'lishi kerak", MaxPrice)
)

// CharityEnabled — hayriya yoqilganmi (CHARITY_PERCENT > 0).
func (s *Service) CharityEnabled() bool { return s != nil && s.CharityPercent > 0 }

// CharityShare — amount Stars'lik obuna to'lovidan hayriya ulushi (pastga yaxlitlangan).
func (s *Service) CharityShare(amount int) int {
	if !s.CharityEnabled() {
		return 0
	}
	return amount * s.CharityPercent / 100
}

// IsDonation — payload hayriya xayriyasiga tegishlimi.
func IsDonation(payload string) bool { return strings.HasPrefix(payload, store.DonationPrefix) }

func newDonationPayload(owner int64) (string, error) {
	nonce, err := randomHex(8)
	if err != nil {
		return "", err
	}
	return store.DonationPrefix + strconv.FormatInt(owner, 10) + ":" + nonce, nil
}

func parseDonation(payload string) (int64, bool) {
	rest, ok := strings.CutPrefix(payload, store.DonationPrefix)
	if !ok {
		return 0, false
	}
	sid, nonce, ok := strings.Cut(rest, ":")
	if !ok || len(nonce) != 16 {
		return 0, false
	}
	id, err := strconv.ParseInt(sid, 10, 64)
	return id, err == nil && id > 0
}

// DonationLink — hayriyaga stars ⭐ qo'shish uchun bir martalik to'lov havolasi (admin ham, mijoz ham).
// Stars to'lovchining Telegram balansidan bot balansiga o'tadi va to'liq hayriya hisobiga yoziladi.
func (s *Service) DonationLink(ctx context.Context, owner int64, stars int) (string, error) {
	if !s.CharityEnabled() {
		return "", ErrCharityDisabled
	}
	if stars < 1 || stars > MaxPrice {
		return "", ErrDonationAmount
	}
	payload, err := newDonationPayload(owner)
	if err != nil {
		return "", err
	}
	desc := "Bu Stars to'liq hayriyaga o'tkaziladi. Hisobot va isbotlar — ilovada «Hayriya» bo'limida."
	if s.CharityName != "" {
		desc = "Bu Stars to'liq «" + s.CharityName + "» hayriyasiga o'tkaziladi. Hisobot va isbotlar — ilovada."
	}
	return s.API.CreateInvoiceLink(ctx, InvoiceLink{
		Title: "Kotib — hayriya", Description: desc, Payload: payload, Label: "Hayriya", Amount: stars,
	})
}

func (s *Service) preCheckoutDonation(from int64, currency string, amount int, payload string) error {
	if !s.CharityEnabled() {
		return ErrCharityDisabled
	}
	if owner, ok := parseDonation(payload); !ok || owner != from {
		return errors.New("bu to'lov havolasi sizga tegishli emas")
	}
	if currency != Currency || amount < 1 || amount > MaxPrice {
		return ErrDonationAmount
	}
	return nil
}

func (s *Service) donationReceived(ctx context.Context, from int64, p PaymentInfo) error {
	if owner, ok := parseDonation(p.Payload); !ok || owner != from {
		s.log().Warn("xayriya payload egasi mos emas", "from", from)
	}
	added, err := s.Store.AddPayment(ctx, store.Payment{
		ChargeID: p.ChargeID, OwnerID: from, Amount: p.Amount, Currency: p.Currency, Payload: p.Payload,
	})
	if err != nil || !added {
		return err
	}
	s.Store.Audit(ctx, from, "charity_donation", fmt.Sprintf("%d %s", p.Amount, p.Currency))
	return nil
}

// monthStart — t oyining boshi (Toshkent vaqti).
func monthStart(t time.Time) time.Time {
	t = t.In(quotaZone)
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, quotaZone)
}

func period(t time.Time) string { return t.In(quotaZone).Format("2006-01") }

// ParseCharityStart — CHARITY_START ("2026-11") → oy boshi; bo'sh → nol vaqt.
func ParseCharityStart(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation("2006-01", v, quotaZone)
	if err != nil {
		return time.Time{}, fmt.Errorf("CHARITY_START YYYY-MM ko'rinishida bo'lishi kerak: %q", v)
	}
	return t, nil
}

// charityStart — hisob boshlangan oy: birinchi marta CharityStart (berilmasa joriy oy) bazaga yoziladi
// va keyin o'zgarmaydi — qayta ishga tushirish hisobni siljitmasin.
func (s *Service) charityStart(ctx context.Context) (time.Time, error) {
	cand := s.CharityStartAt
	if cand.IsZero() {
		cand = s.now()
	}
	t, err := s.Store.CharityStart(ctx, monthStart(cand))
	if err != nil {
		return t, err
	}
	return monthStart(t), nil
}

// owed — start'dan to gacha hayriyaga ajratilishi kerak bo'lgan jami (obunalar × foiz + xayriyalar).
func (s *Service) owed(ctx context.Context, start, to time.Time) (int64, error) {
	subs, dons, err := s.Store.PaymentSums(ctx, start, to)
	return subs*int64(s.CharityPercent)/100 + dons, err
}

// CloseCharityMonths tugagan oylar uchun hayriya summasini hisoblab yozadi (har oy bir marta) va
// adminlarga (Exempt — ADMIN_IDS) xabar yuboradi. Yangi yozilgan hisoblarni qaytaradi.
func (s *Service) CloseCharityMonths(ctx context.Context, notify Notifier) []store.CharityPayout {
	if !s.CharityEnabled() {
		return nil
	}
	start, err := s.charityStart(ctx)
	if err != nil {
		s.log().Warn("charity start", "err", err)
		return nil
	}
	all, err := s.Store.ListCharityPayouts(ctx, 0)
	if err != nil {
		s.log().Warn("charity payouts", "err", err)
		return nil
	}
	done := map[string]bool{}
	var prev int64 // avvalgi oylarga hisoblangan jami
	for _, p := range all {
		if p.Kind == store.CharityMonthly {
			done[p.Period] = true
			prev += int64(p.Stars)
		}
	}
	var out []store.CharityPayout
	cur := monthStart(s.now())
	for m := start; m.Before(cur); m = m.AddDate(0, 1, 0) {
		if done[period(m)] {
			continue
		}
		next := m.AddDate(0, 1, 0)
		total, err := s.owed(ctx, start, next)
		if err != nil {
			s.log().Warn("charity owed", "err", err)
			return out
		}
		subs, dons, err := s.Store.PaymentSums(ctx, m, next)
		if err != nil {
			s.log().Warn("charity month", "err", err)
			return out
		}
		p := store.CharityPayout{Kind: store.CharityMonthly, Period: period(m),
			Share: int(subs) * s.CharityPercent / 100, Donated: int(dons), Stars: int(max(total-prev, 0))}
		p.Adjust = p.Stars - p.Share - p.Donated
		id, added, err := s.Store.AddCharityPayout(ctx, p)
		if err != nil {
			s.log().Warn("charity add", "period", p.Period, "err", err)
			return out
		}
		if !added {
			continue // boshqa nusxa yozib bo'ldi
		}
		p.ID = id
		prev += int64(p.Stars)
		out = append(out, p)
		if notify != nil && p.Stars > 0 {
			for admin := range s.Exempt {
				notify(ctx, admin, CharityMonthText(p), true)
			}
		}
	}
	return out
}

// CharityMonthText — oylik hisob haqida adminga xabar.
func CharityMonthText(p store.CharityPayout) string {
	text := fmt.Sprintf("💚 Hayriya hisobi — %s (#%d)\n\nObunalardan ulush: %d ⭐\nXayriyalar: %d ⭐\n", p.Period, p.ID, p.Share, p.Donated)
	if p.Adjust != 0 {
		text += fmt.Sprintf("Tuzatish (refund/yaxlitlash): %+d ⭐\n", p.Adjust)
	}
	return text + fmt.Sprintf("Jami o'tkazish kerak: %d ⭐\n\nStars'ni @BotFather → Balance (Fragment) orqali yechib, tashkilotga o'tkazing, "+
		"keyin isbotini kiriting: /hayriya paid %d <tx hash yoki havola>", p.Stars, p.ID)
}

// CharityPayoutView — Mini App va bot uchun o'tkazma.
type CharityPayoutView struct {
	ID      int64      `json:"id"`
	Kind    string     `json:"kind"`
	Period  string     `json:"period"`
	Share   int        `json:"share"`
	Donated int        `json:"donated"`
	Adjust  int        `json:"adjust"`
	Stars   int        `json:"stars"`
	Note    string     `json:"note"`
	Proof   string     `json:"proof"`
	Paid    bool       `json:"paid"`
	PaidAt  *time.Time `json:"paid_at"`
}

// CharityReport — hayriya holati (hamma uchun ochiq: shaffoflik).
type CharityReport struct {
	Enabled bool   `json:"enabled"`
	Percent int    `json:"percent"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	// PaidStars — tashkilotga o'tkazilgani (oylik + adminning qo'shimcha xayriyasi).
	PaidStars int64 `json:"paid_stars"`
	// PendingStars — hisoblangan, lekin hali o'tkazilmagan oylar.
	PendingStars int64 `json:"pending_stars"`
	// AccruingStars — joriy (tugamagan) oyda yig'ilayotgan taxminiy summa.
	AccruingStars int64               `json:"accruing_stars"`
	MaxDonation   int                 `json:"max_donation"`
	Payouts       []CharityPayoutView `json:"payouts"`
}

func (s *Service) CharityReport(ctx context.Context) (CharityReport, error) {
	r := CharityReport{Payouts: []CharityPayoutView{}}
	if !s.CharityEnabled() {
		return r, nil
	}
	r.Enabled, r.Percent, r.Name, r.URL, r.MaxDonation = true, s.CharityPercent, s.CharityName, s.CharityURL, MaxPrice
	all, err := s.Store.ListCharityPayouts(ctx, 0)
	if err != nil {
		return r, err
	}
	var monthly int64
	for i, p := range all {
		if p.Kind == store.CharityMonthly {
			monthly += int64(p.Stars)
		}
		if p.Paid {
			r.PaidStars += int64(p.Stars)
		} else if p.Kind == store.CharityMonthly {
			r.PendingStars += int64(p.Stars)
		}
		if i < charityHistory && p.Stars > 0 { // tushumsiz oylar ro'yxatni to'ldirmasin
			r.Payouts = append(r.Payouts, CharityPayoutView{p.ID, p.Kind, p.Period, p.Share, p.Donated, p.Adjust, p.Stars,
				p.Note, p.Proof, p.Paid, p.PaidAt})
		}
	}
	start, err := s.charityStart(ctx)
	if err != nil {
		return r, err
	}
	// Tugagan, lekin hali hisoblanmagan oylar ham shu yerga kiradi (CloseCharityMonths soatiga bir ishlaydi).
	total, err := s.owed(ctx, start, s.now().Add(time.Second))
	if err != nil {
		return r, err
	}
	r.AccruingStars = max(total-monthly, 0)
	return r, nil
}

func cleanText(v string, n int) (string, error) {
	v = strings.TrimSpace(v)
	if utf8.RuneCountInString(v) > n || strings.ContainsAny(v, "\n\r") {
		return "", fmt.Errorf("matn bir qatorda va %d belgigacha bo'lishi kerak", n)
	}
	return v, nil
}

// MarkCharityPaid — admin oylik summani tashkilotga o'tkazganini isboti (tx hash / havola) bilan belgilaydi.
func (s *Service) MarkCharityPaid(ctx context.Context, id int64, proof string, admin int64) error {
	if !s.CharityEnabled() {
		return ErrCharityDisabled
	}
	proof, err := cleanText(proof, maxProof)
	if err != nil {
		return err
	}
	if proof == "" {
		return errors.New("isbot (tx hash yoki havola) kerak")
	}
	if err := s.Store.MarkCharityPaid(ctx, id, proof, admin, s.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errors.New("o'tkazma topilmadi yoki allaqachon to'langan")
		}
		return err
	}
	s.log().Info("charity paid", "id", id, "admin", admin)
	if all, err := s.Store.ListCharityPayouts(ctx, 0); err == nil {
		for _, p := range all {
			if p.ID == id && p.Kind == store.CharityMonthly {
				go s.notifySubscribers(context.WithoutCancel(ctx), p)
			}
		}
	}
	return nil
}

// charityNotifyPause — Telegram bot limiti (~30 xabar/s) ichida.
var charityNotifyPause = 50 * time.Millisecond

var monthNames = [...]string{"yanvar", "fevral", "mart", "aprel", "may", "iyun", "iyul", "avgust", "sentabr", "oktabr", "noyabr", "dekabr"}

// periodName — "2026-10" → "oktabr 2026".
func periodName(period string) string {
	t, err := ParseCharityStart(period)
	if err != nil || t.IsZero() {
		return period
	}
	return fmt.Sprintf("%s %d", monthNames[t.Month()-1], t.Year())
}

// notifySubscribers — oylik summa o'tkazilgach, shu oyda obuna to'lagan har mijozga uning to'lovidan
// ajratilgan ulush tashkilotga yetib borgani haqida xabar (faqat obunachilar: qoida — obuna narxining foizi).
func (s *Service) notifySubscribers(ctx context.Context, p store.CharityPayout) {
	if s.Notify == nil {
		return
	}
	from, err := ParseCharityStart(p.Period)
	if err != nil || from.IsZero() {
		return
	}
	subs, err := s.Store.SubscriberSums(ctx, from, from.AddDate(0, 1, 0))
	if err != nil {
		s.log().Warn("charity subscribers", "period", p.Period, "err", err)
		return
	}
	to := ""
	if s.CharityName != "" {
		to = " («" + s.CharityName + "»)"
	}
	sent := 0
	for owner, amount := range subs {
		share := s.CharityShare(int(amount))
		if share <= 0 || s.Banned(ctx, owner) {
			continue
		}
		if _, err := s.Store.GetAccount(ctx, owner); err != nil {
			continue // akkauntini o'chirgan
		}
		s.Notify(ctx, owner, fmt.Sprintf("💚 %s oyidagi obuna to'lovingizdan ajratilgan %d ⭐ hayriyaga%s o'tkazildi. Rahmat!\n\nIsbot: %s\nHisobot: /hayriya",
			periodName(p.Period), share, to, p.Proof), false)
		sent++
		time.Sleep(charityNotifyPause)
	}
	s.log().Info("charity subscribers notified", "period", p.Period, "sent", sent)
}

// MyCharity — egasining hissasi (hisob boshlanganidan beri): obunalaridan ulush + xayriyalari.
func (s *Service) MyCharity(ctx context.Context, owner int64) (int64, error) {
	if !s.CharityEnabled() {
		return 0, nil
	}
	start, err := s.charityStart(ctx)
	if err != nil {
		return 0, err
	}
	subs, dons, err := s.Store.OwnerPaymentSums(ctx, owner, start, s.now().Add(time.Second))
	return subs*int64(s.CharityPercent)/100 + dons, err
}

// AddManualCharity — admin o'z hisobidan (pul/TON bilan, Stars'siz) qilgan xayriyani yozadi.
// Stars ekvivalenti hisobotda ko'rsatiladi; oylik majburiyatni kamaytirmaydi — qo'shimcha xayriya.
func (s *Service) AddManualCharity(ctx context.Context, admin int64, stars int, note, proof string) (int64, error) {
	if !s.CharityEnabled() {
		return 0, ErrCharityDisabled
	}
	if stars < 1 || stars > 10_000_000 {
		return 0, errors.New("summa (Stars ekvivalenti) 1–10 000 000 bo'lishi kerak")
	}
	note, err := cleanText(note, maxCharityNote)
	if err != nil {
		return 0, err
	}
	if proof, err = cleanText(proof, maxProof); err != nil {
		return 0, err
	}
	at := s.now()
	id, _, err := s.Store.AddCharityPayout(ctx, store.CharityPayout{Kind: store.CharityManual, Stars: stars, Note: note,
		Proof: proof, Paid: true, ByAdmin: admin, PaidAt: &at})
	if err == nil {
		s.log().Info("charity manual", "id", id, "admin", admin, "stars", stars)
	}
	return id, err
}
