package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
)

// Markets — oltin, kumush va kriptovalyuta narxlari (kalitsiz): gold-api.com (zaxira — CoinGecko
// PAXG), CoinGecko va so'mdagi narx uchun Markaziy bank dollar kursi.
type Markets struct {
	HTTP *http.Client
	// Testlar uchun manzillar (bo'sh — haqiqiy xizmatlar).
	GoldURL, CoinURL, CBUURL string
}

// troyOunce — bir troy unsiya grammda.
const troyOunce = 31.1034768

// coinIDs — qo'llanadigan kriptovalyutalar: belgi → CoinGecko ID (ixtiyoriy ID yo'q).
var coinIDs = map[string]string{
	"BTC": "bitcoin", "ETH": "ethereum", "USDT": "tether", "BNB": "binancecoin", "SOL": "solana",
	"TON": "the-open-network", "XRP": "ripple", "DOGE": "dogecoin", "TRX": "tron", "ADA": "cardano",
	"NOT": "notcoin", "LTC": "litecoin",
}

func coinList() string {
	ks := make([]string, 0, len(coinIDs))
	for k := range coinIDs {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ", ")
}

// parseCoins — "BTC, eth" → belgilar (noma'lumlari xato).
func parseCoins(v string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, s := range strings.FieldsFunc(strings.ToUpper(v), func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if coinIDs[s] == "" {
			return nil, &ErrInvalid{"noma'lum kriptovalyuta: " + s + " (mavjudlari: " + coinList() + ")"}
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *Markets) Info() Info {
	return Info{ID: "markets", Name: "Oltin va kripto", Icon: "🪙", Category: "Moliya",
		Description: "Oltin va kumush narxi (grammi so'mda, Markaziy bank kursi bo'yicha) hamda Bitcoin, Ethereum, TON kabi kriptovalyutalar narxi va kunlik o'zgarishi. Kalit shart emas.",
		Fields: []Field{
			{Key: "coins", Label: "Kuzatiladigan kriptovalyutalar", Type: FieldText, Default: "BTC, ETH, TON", Placeholder: "BTC, ETH, TON",
				Help: "Mavjudlari: " + coinList() + "."},
		},
		Examples: []string{"Har kuni 10:00 da oltin grammi narxi so'mda", "Har kuni 09:00 da Bitcoin va TON narxi — 5% dan ko'p o'zgarsa, alohida ta'kidla"}}
}

func (m *Markets) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{
		{Name: "gold_price", Description: "Oltin va kumush jahon narxi: troy unsiya (dollar), 1 gramm dollar va so'mda (Markaziy bank kursi bo'yicha).",
			Params: obj(map[string]any{})},
		{Name: "crypto_price", Description: "Kriptovalyutalar narxi (dollar) va 24 soatlik o'zgarishi.",
			Params: obj(map[string]any{"coins": str("Belgilar vergul bilan, masalan BTC,ETH (ixtiyoriy, standart — egasi tanlaganlar). Mavjudlari: " + coinList())})},
	}
}

func (m *Markets) Validate(cfg Config) error {
	_, err := parseCoins(cfg["coins"])
	return err
}

func (m *Markets) Call(ctx context.Context, cfg Config, tool string, args map[string]any) (string, error) {
	if tool == "gold_price" {
		return m.gold(ctx)
	}
	v := argString(args, "coins")
	if v == "" {
		v = cfg["coins"]
	}
	if v == "" {
		v = "BTC, ETH, TON"
	}
	coins, err := parseCoins(v)
	if err != nil {
		return "", err
	}
	if len(coins) == 0 {
		return "", errors.New("kriptovalyuta tanlanmagan")
	}
	return m.crypto(ctx, coins)
}

func (m *Markets) coinBase() string {
	if m.CoinURL != "" {
		return strings.TrimRight(m.CoinURL, "/")
	}
	return "https://api.coingecko.com/api/v3"
}

// coinPrices — CoinGecko: ID → {usd, usd_24h_change}.
func (m *Markets) coinPrices(ctx context.Context, ids []string) (map[string]map[string]float64, error) {
	q := url.Values{"ids": {strings.Join(ids, ",")}, "vs_currencies": {"usd"}, "include_24hr_change": {"true"}}
	data, err := getBody(ctx, m.HTTP, m.coinBase()+"/simple/price?"+q.Encode(), map[string]string{"Accept": "application/json"})
	if err != nil {
		return nil, errors.New("kripto narxlarini olib bo'lmadi, birozdan keyin urinib ko'ring")
	}
	out := map[string]map[string]float64{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, errors.New("kripto narxlarini o'qib bo'lmadi")
	}
	return out, nil
}

func (m *Markets) crypto(ctx context.Context, coins []string) (string, error) {
	ids := make([]string, len(coins))
	for i, c := range coins {
		ids[i] = coinIDs[c]
	}
	prices, err := m.coinPrices(ctx, ids)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("Kriptovalyuta narxlari (CoinGecko):\n")
	for _, c := range coins {
		p, ok := prices[coinIDs[c]]
		if !ok {
			fmt.Fprintf(&b, "%s: ma'lumot yo'q\n", c)
			continue
		}
		fmt.Fprintf(&b, "%s: $%s (24 soatda %+.1f%%)\n", c, money(p["usd"]), p["usd_24h_change"])
	}
	return b.String(), nil
}

// money — narxni o'qiladigan ko'rinishda: katta sonlar butun, kichiklari kasr bilan.
func money(v float64) string {
	switch {
	case v >= 1000:
		return groupThousands(strconv.FormatFloat(v, 'f', 0, 64))
	case v >= 1:
		return strconv.FormatFloat(v, 'f', 2, 64)
	}
	return strconv.FormatFloat(v, 'f', 6, 64)
}

// groupThousands — "1234567" → "1 234 567".
func groupThousands(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// metal — gold-api.com dan bir troy unsiya narxi (dollar).
func (m *Markets) metal(ctx context.Context, sym string) (float64, error) {
	base := m.GoldURL
	if base == "" {
		base = "https://api.gold-api.com/price"
	}
	data, err := getBody(ctx, m.HTTP, strings.TrimRight(base, "/")+"/"+sym, nil)
	if err != nil {
		return 0, err
	}
	var r struct {
		Price float64 `json:"price"`
	}
	if json.Unmarshal(data, &r) != nil || r.Price <= 0 {
		return 0, errors.New("narx yo'q")
	}
	return r.Price, nil
}

// usdRate — Markaziy bankning dollar kursi (so'm); xato bo'lsa 0.
func (m *Markets) usdRate(ctx context.Context) float64 {
	base := m.CBUURL
	if base == "" {
		base = "https://cbu.uz/uz/arkhiv-kursov-valyut/json/"
	}
	data, err := getBody(ctx, m.HTTP, base, nil)
	if err != nil {
		return 0
	}
	var rows []struct {
		Ccy  string `json:"Ccy"`
		Rate string `json:"Rate"`
	}
	if json.Unmarshal(data, &rows) != nil {
		return 0
	}
	for _, r := range rows {
		if r.Ccy == "USD" {
			v, _ := strconv.ParseFloat(r.Rate, 64)
			return v
		}
	}
	return 0
}

func (m *Markets) gold(ctx context.Context) (string, error) {
	gold, err := m.metal(ctx, "XAU")
	if err != nil {
		// Zaxira: PAX Gold tokeni (1 token = 1 troy unsiya oltin) CoinGecko'da.
		if p, cerr := m.coinPrices(ctx, []string{"pax-gold"}); cerr == nil && p["pax-gold"]["usd"] > 0 {
			gold, err = p["pax-gold"]["usd"], nil
		}
	}
	if err != nil {
		return "", errors.New("oltin narxini olib bo'lmadi, birozdan keyin urinib ko'ring")
	}
	silver, _ := m.metal(ctx, "XAG")
	rate := m.usdRate(ctx)
	var b strings.Builder
	line := func(name string, oz float64) {
		g := oz / troyOunce
		fmt.Fprintf(&b, "%s: 1 troy unsiya $%s, 1 gramm $%.2f", name, money(oz), g)
		if rate > 0 {
			fmt.Fprintf(&b, " ≈ %s so'm", groupThousands(strconv.FormatFloat(g*rate, 'f', 0, 64)))
		}
		b.WriteString("\n")
	}
	line("Oltin (999)", gold)
	if silver > 0 {
		line("Kumush (999)", silver)
	}
	if rate > 0 {
		fmt.Fprintf(&b, "So'mdagi narx Markaziy bank kursi bo'yicha (1$ = %s so'm). Bank quymalari va zargarlik narxi farq qiladi.", money(rate))
	} else {
		b.WriteString("So'mdagi narx hisoblanmadi (Markaziy bank kursi olinmadi).")
	}
	return b.String(), nil
}
