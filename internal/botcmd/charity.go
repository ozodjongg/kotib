package botcmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
	"github.com/ozodjongg/secretar_ai_agent/internal/bot"
)

// donationPresets — /hayriya tugmalaridagi tayyor summalar.
var donationPresets = []int{50, 100, 500}

const charityAdminHelp = `Admin:
/hayriya <summa> — o'z balansingizdan Stars qo'shish
/hayriya paid <#id> <tx hash yoki havola> — oylik summa o'tkazildi
/hayriya manual <summa> <izoh> — Stars'siz (pul/TON) qilgan xayriyangizni yozish`

// charity — /hayriya: hisobot va xayriya havolalari; admin uchun o'tkazmani belgilash.
func (h *Handler) charity(ctx context.Context, from int64, args string) bot.Reply {
	if !h.Billing.CharityEnabled() {
		return bot.Reply{Text: "Hayriya hozircha yoqilmagan."}
	}
	fields := strings.Fields(args)
	if len(fields) > 0 && h.Admins[from] {
		switch fields[0] {
		case "paid":
			return h.charityPaid(ctx, from, fields[1:])
		case "manual":
			return h.charityManual(ctx, from, fields[1:])
		}
	}
	if len(fields) == 1 {
		n, err := strconv.Atoi(strings.TrimSuffix(fields[0], "⭐"))
		if err != nil {
			return bot.Reply{Text: "Foydalanish: /hayriya yoki /hayriya 100"}
		}
		link, err := h.Billing.DonationLink(ctx, from, n)
		if err != nil {
			return h.donationErr(from, err)
		}
		return bot.Reply{Text: fmt.Sprintf("💚 Hayriyaga %d ⭐ — to'liq tashkilotga o'tkaziladi.", n), ButtonURL: link,
			Button: fmt.Sprintf("⭐ %d — hayriya qilish", n)}
	}
	if len(fields) > 1 {
		return bot.Reply{Text: "Foydalanish: /hayriya yoki /hayriya 100"}
	}
	r, err := h.Billing.CharityReport(ctx)
	if err != nil {
		h.log().Warn("charity report", "err", err)
		return bot.Reply{Text: "Xatolik, keyinroq urinib ko'ring."}
	}
	var links []bot.Link
	for _, n := range donationPresets {
		link, err := h.Billing.DonationLink(ctx, from, n)
		if err != nil {
			return h.donationErr(from, err)
		}
		links = append(links, bot.Link{Text: fmt.Sprintf("💚 %d ⭐", n), URL: link})
	}
	text := charityText(r)
	if mine, err := h.Billing.MyCharity(ctx, from); err == nil && mine > 0 {
		text = strings.Replace(text, "O'tkazilgan:", fmt.Sprintf("Sizning hissangiz: %d ⭐\n\nO'tkazilgan:", mine), 1)
	}
	if h.Admins[from] {
		for _, p := range r.Payouts {
			if !p.Paid && p.Stars > 0 {
				text += fmt.Sprintf("\n⏳ #%d · %s — %d ⭐ o'tkazilmagan", p.ID, p.Period, p.Stars)
			}
		}
		text += "\n\n" + charityAdminHelp
	}
	return bot.Reply{Text: text, Links: links, OpenApp: true}
}

func (h *Handler) donationErr(from int64, err error) bot.Reply {
	if errors.Is(err, billing.ErrDonationAmount) {
		return bot.Reply{Text: err.Error()}
	}
	h.log().Warn("donation link", "owner", from, "err", err)
	return bot.Reply{Text: "To'lov havolasini yaratib bo'lmadi, keyinroq urinib ko'ring."}
}

func charityText(r billing.CharityReport) string {
	var b strings.Builder
	b.WriteString("💚 Hayriya\n\n")
	fmt.Fprintf(&b, "Har obuna to'lovining %d%% i hayriyaga ajratiladi", r.Percent)
	if r.Name != "" {
		fmt.Fprintf(&b, " («%s»)", r.Name)
	}
	b.WriteString(". Oyda bir marta tashkilotga o'tkaziladi, isbotlari ilovada — «Hayriya» bo'limida.\n\n")
	fmt.Fprintf(&b, "O'tkazilgan: %d ⭐\n", r.PaidStars)
	if r.PendingStars > 0 {
		fmt.Fprintf(&b, "O'tkazilishi kutilmoqda: %d ⭐\n", r.PendingStars)
	}
	fmt.Fprintf(&b, "Shu oy yig'ilmoqda: %d ⭐\n", r.AccruingStars)
	if r.URL != "" {
		b.WriteString(r.URL + "\n")
	}
	b.WriteString("\nO'zingiz ham qo'shishingiz mumkin — Stars to'liq hayriyaga ketadi:")
	return b.String()
}

func (h *Handler) charityPaid(ctx context.Context, admin int64, args []string) bot.Reply {
	if len(args) < 2 {
		return bot.Reply{Text: "Foydalanish: /hayriya paid <#id> <tx hash yoki havola>"}
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(args[0], "#"), 10, 64)
	if err != nil || id <= 0 {
		return bot.Reply{Text: "ID noto'g'ri"}
	}
	if err := h.Billing.MarkCharityPaid(ctx, id, strings.Join(args[1:], " "), admin); err != nil {
		return bot.Reply{Text: "Belgilab bo'lmadi: " + err.Error()}
	}
	h.Store.Audit(ctx, admin, "charity_paid", "#"+strconv.FormatInt(id, 10))
	return bot.Reply{Text: fmt.Sprintf("✅ #%d o'tkazilgan deb belgilandi — hisobotda isboti bilan ko'rinadi.", id)}
}

func (h *Handler) charityManual(ctx context.Context, admin int64, args []string) bot.Reply {
	if len(args) < 1 {
		return bot.Reply{Text: "Foydalanish: /hayriya manual <summa ⭐ ekvivalenti> <izoh>"}
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		return bot.Reply{Text: "Summa noto'g'ri"}
	}
	id, err := h.Billing.AddManualCharity(ctx, admin, n, strings.Join(args[1:], " "), "")
	if err != nil {
		return bot.Reply{Text: "Yozib bo'lmadi: " + err.Error()}
	}
	h.Store.Audit(ctx, admin, "charity_manual", strconv.Itoa(n))
	return bot.Reply{Text: fmt.Sprintf("💚 Qo'shimcha xayriya yozildi (#%d, %d ⭐ ekvivalenti). Rahmat!", id, n)}
}
