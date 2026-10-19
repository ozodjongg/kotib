package plugin

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
)

// Holidays — O'zbekiston bayramlari va maxsus kunlari, Ramazon oyi (internet kerak emas: sanalar
// kod ichida). Hijriy sanalar astronomik hisob bo'yicha taxminiy — rasmiy sanani O'zbekiston
// musulmonlari idorasi e'lon qiladi.
type Holidays struct {
	Now func() time.Time // test uchun; nil — time.Now
}

// holiday — sana va nomi; off — rasmiy dam olish kuni.
type holiday struct {
	date time.Time
	name string
	off  bool
}

// fixedHolidays — har yili bir xil sanada (oy, kun).
var fixedHolidays = []struct {
	m    time.Month
	d    int
	name string
	off  bool
}{
	{time.January, 1, "Yangi yil", true},
	{time.January, 14, "Vatan himoyachilari kuni", false},
	{time.March, 8, "Xalqaro xotin-qizlar kuni", true},
	{time.March, 21, "Navro'z bayrami", true},
	{time.May, 9, "Xotira va qadrlash kuni", true},
	{time.June, 1, "Xalqaro bolalarni himoya qilish kuni", false},
	{time.June, 27, "Matbuot va ommaviy axborot vositalari xodimlari kuni", false},
	{time.August, 31, "Qatag'on qurbonlarini yod etish kuni", false},
	{time.September, 1, "Mustaqillik kuni", true},
	{time.October, 1, "O'qituvchi va murabbiylar kuni", true},
	{time.October, 21, "O'zbek tili bayrami kuni", false},
	{time.December, 8, "Konstitutsiya kuni", true},
}

// islamicDates — Ramazon boshlanishi, Ramazon hayiti, Qurbon hayiti (taxminiy, oy, kun).
var islamicDates = map[int][3][2]int{
	2025: {{3, 1}, {3, 30}, {6, 6}},
	2026: {{2, 18}, {3, 20}, {5, 27}},
	2027: {{2, 8}, {3, 10}, {5, 16}},
	2028: {{1, 28}, {2, 27}, {5, 5}},
	2029: {{1, 16}, {2, 15}, {4, 24}},
	2030: {{1, 6}, {2, 5}, {4, 13}},
}

var uzWeekdays = [...]string{"yakshanba", "dushanba", "seshanba", "chorshanba", "payshanba", "juma", "shanba"}

func uzWeekday(t time.Time) string { return uzWeekdays[t.Weekday()] }

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, uzLoc) }

// holidaysOf — yilning barcha bayram va maxsus kunlari (sana bo'yicha tartiblangan).
func holidaysOf(y int) []holiday {
	var out []holiday
	for _, f := range fixedHolidays {
		out = append(out, holiday{day(y, f.m, f.d), f.name, f.off})
	}
	if is, ok := islamicDates[y]; ok {
		out = append(out,
			holiday{day(y, time.Month(is[0][0]), is[0][1]), "Ramazon oyi boshlanishi (taxminiy)", false},
			holiday{day(y, time.Month(is[1][0]), is[1][1]), "Ramazon hayiti (taxminiy sana)", true},
			holiday{day(y, time.Month(is[2][0]), is[2][1]), "Qurbon hayiti (taxminiy sana)", true})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].date.Before(out[j].date) })
	return out
}

func (h *Holidays) Info() Info {
	return Info{ID: "holidays", Name: "Bayramlar va maxsus kunlar", Icon: "📅", Category: "Kundalik",
		Description: "O'zbekiston bayramlari, dam olish kunlari va Ramazon oyi: «ertaga dam olish kuni», «hayitga 5 kun qoldi». Internet kerak emas; hayit sanalari taxminiy.",
		Examples:    []string{"Har dushanba 08:00 da shu va keyingi haftadagi bayramlar", "Har kuni 20:00 da — ertaga bayram bo'lsa, eslat"}}
}

func (h *Holidays) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{{Name: "holidays", Description: "Bugungi va yaqin kunlardagi O'zbekiston bayramlari (dam olish kuni yoki yo'qligi, necha kun qolgani), Ramazon oyi holati.",
		Params: obj(map[string]any{"days": map[string]any{"type": "INTEGER", "description": "Necha kun oldinga qarash, 1–366, standart 30"}})}}
}

func (h *Holidays) Call(_ context.Context, _ Config, _ string, args map[string]any) (string, error) {
	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	t := now().In(uzLoc)
	today := day(t.Year(), t.Month(), t.Day())
	end := today.AddDate(0, 0, argInt(args, "days", 30, 1, 366))
	var b strings.Builder
	fmt.Fprintf(&b, "Bugun %s, %s.\n", today.Format("02.01.2006"), uzWeekday(today))

	// Ramazon oyi: boshlanishidan hayitgacha.
	if is, ok := islamicDates[today.Year()]; ok {
		start, eid := day(today.Year(), time.Month(is[0][0]), is[0][1]), day(today.Year(), time.Month(is[1][0]), is[1][1])
		if !today.Before(start) && today.Before(eid) {
			fmt.Fprintf(&b, "Hozir Ramazon oyi (taxminan %d-kun), Ramazon hayitigacha %d kun. Saharlik va iftorlik vaqtlari — namoz vaqtlari plaginida (Bomdod va Shom).\n",
				int(today.Sub(start).Hours()/24)+1, int(eid.Sub(today).Hours()/24))
		}
	}
	var list []holiday
	for y := today.Year(); y <= end.Year(); y++ {
		for _, hd := range holidaysOf(y) {
			if !hd.date.Before(today) && !hd.date.After(end) {
				list = append(list, hd)
			}
		}
	}
	if len(list) == 0 {
		fmt.Fprintf(&b, "Keyingi %d kunda bayram yo'q.\n", int(end.Sub(today).Hours()/24))
	}
	for _, hd := range list {
		n := int(math.Round(hd.date.Sub(today).Hours() / 24))
		when := fmt.Sprintf("%d kundan keyin", n)
		switch n {
		case 0:
			when = "BUGUN"
		case 1:
			when = "ertaga"
		}
		off := ""
		if hd.off {
			off = " — dam olish kuni"
		}
		fmt.Fprintf(&b, "• %s (%s, %s) — %s%s\n", hd.name, hd.date.Format("02.01.2006"), uzWeekday(hd.date), when, off)
	}
	b.WriteString("Eslatma: bayram dam olish kunlariga to'g'ri kelsa, qo'shimcha dam olish kunlari hukumat qarori bilan belgilanadi; hayit sanalari taxminiy.")
	return b.String(), nil
}
