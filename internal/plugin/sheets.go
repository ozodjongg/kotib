package plugin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
)

// Sheets — Google Sheets jadvallarini o'qish (Google akkaunt orqali, faqat o'qish ruxsati):
// buyurtmalar, xarajatlar, ombor — AI hisobot va xulosa tayyorlaydi.
type Sheets struct {
	Token   func(ctx context.Context, refreshToken string) (string, error)
	HTTP    *http.Client
	BaseURL string // default https://sheets.googleapis.com/v4/spreadsheets
}

func sheetsPlugin(o Options) Plugin {
	if o.SheetsToken == nil {
		return soon("sheets", "Google Sheets", "📊", "Hujjatlar", "Buyurtma, xarajat va ombor jadvallaringizdan hisobot.")
	}
	return &Sheets{Token: o.SheetsToken, HTTP: o.HTTP, BaseURL: o.SheetsURL}
}

func (s *Sheets) Info() Info {
	return Info{ID: "sheets", Name: "Google Sheets", Icon: "📊", Category: "Hujjatlar", Connect: "google",
		Description: "Google jadvallaringizni (buyurtmalar, xarajatlar, ombor) o'qib, hisobot va xulosa tayyorlaydi. Faqat o'qish — jadvalga hech narsa yozilmaydi.",
		Fields: []Field{{Key: "urls", Label: "Jadval havolasi", Type: FieldText, Required: true,
			Placeholder: "https://docs.google.com/spreadsheets/d/…",
			Help:        "Jadvalni brauzerda oching va manzilini nusxalab shu yerga qo'ying. 3 tagacha jadval — bo'sh joy bilan ajrating."}},
		Examples: []string{"Har kuni 21:00 da bugungi buyurtmalar soni va summasi", "Har dushanba — o'tgan hafta xarajatlari toifalar bo'yicha"}}
}

func (s *Sheets) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{{Name: "sheets_read", Description: "Egasining Google jadvalidan qatorlarni o'qiydi (birinchi qator — sarlavhalar). Varaqlar ro'yxati ham qaytadi.",
		Params: obj(map[string]any{
			"file":     map[string]any{"type": "INTEGER", "description": "Qaysi jadval (1–3), standart 1"},
			"sheet":    str("Varaq nomi (ixtiyoriy, standart — birinchisi)"),
			"max_rows": map[string]any{"type": "INTEGER", "description": "Ko'pi bilan nechta qator, 1–300, standart 150"},
		})}}
}

var sheetIDRe = regexp.MustCompile(`(?:/spreadsheets/d/)?([A-Za-z0-9_-]{20,100})`)

// sheetIDs — sozlamadagi havolalardan jadval ID'lari (3 tagacha).
func sheetIDs(v string) []string {
	var out []string
	for _, f := range strings.Fields(strings.ReplaceAll(v, ",", " ")) {
		if m := sheetIDRe.FindStringSubmatch(f); m != nil {
			out = append(out, m[1])
		}
		if len(out) == 3 {
			break
		}
	}
	return out
}

func (s *Sheets) base() string {
	if s.BaseURL != "" {
		return strings.TrimRight(s.BaseURL, "/")
	}
	return "https://sheets.googleapis.com/v4/spreadsheets"
}

func (s *Sheets) Call(ctx context.Context, cfg Config, _ string, args map[string]any) (string, error) {
	ids := sheetIDs(cfg["urls"])
	if len(ids) == 0 {
		return "", errors.New("jadval havolasi kiritilmagan (Plaginlar → Google Sheets → Sozlash)")
	}
	n := argInt(args, "file", 1, 1, len(ids))
	id := ids[n-1]
	var meta struct {
		Properties struct {
			Title string `json:"title"`
		} `json:"properties"`
		Sheets []struct {
			Properties struct {
				Title string `json:"title"`
			} `json:"properties"`
		} `json:"sheets"`
	}
	if err := oauthGet(ctx, s.HTTP, s.Token, cfg, "Google Sheets", s.base()+"/"+url.PathEscape(id)+"?fields=properties.title,sheets.properties.title", &meta); err != nil {
		return "", err
	}
	if len(meta.Sheets) == 0 {
		return "Jadvalda varaq yo'q.", nil
	}
	var tabs []string
	sheet := meta.Sheets[0].Properties.Title
	want := strings.ToLower(argString(args, "sheet"))
	for _, sh := range meta.Sheets {
		tabs = append(tabs, sh.Properties.Title)
		if want != "" && strings.ToLower(sh.Properties.Title) == want {
			sheet = sh.Properties.Title
		}
	}
	maxRows := argInt(args, "max_rows", 150, 1, 300)
	rng := fmt.Sprintf("'%s'!A1:Z%d", strings.ReplaceAll(sheet, "'", "''"), maxRows+1)
	var vals struct {
		Values [][]any `json:"values"`
	}
	if err := oauthGet(ctx, s.HTTP, s.Token, cfg, "Google Sheets", s.base()+"/"+url.PathEscape(id)+"/values/"+url.PathEscape(rng), &vals); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Jadval «%s», varaq «%s» (varaqlar: %s). %d qator:\n", meta.Properties.Title, sheet, strings.Join(tabs, ", "), len(vals.Values))
	for _, row := range vals.Values {
		cells := make([]string, len(row))
		for i, c := range row {
			cells[i] = strings.ReplaceAll(fmt.Sprint(c), "\n", " ")
		}
		b.WriteString(strings.Join(cells, " | ") + "\n")
		if b.Len() > maxFeedOutput {
			b.WriteString("…(qisqartirildi)")
			break
		}
	}
	return b.String(), nil
}
