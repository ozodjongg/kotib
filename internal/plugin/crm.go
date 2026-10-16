package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
)

// Validator — plagin sozlamasini saqlashdan oldin tekshiradi (xato matni mijozga ko'rsatiladi).
type Validator interface {
	Validate(cfg Config) error
}

func crmTool(name, desc string) llm.ToolDecl {
	return llm.ToolDecl{Name: name, Description: desc, Params: obj(map[string]any{
		"what":  map[string]any{"type": "STRING", "enum": []string{"leads", "deals", "tasks"}, "description": "leads — yangi lidlar, deals — yangi bitimlar, tasks — bugungi va muddati o'tgan vazifalar"},
		"days":  map[string]any{"type": "INTEGER", "description": "Lid/bitimlar: oxirgi necha kun, 1–30, standart 1"},
		"limit": map[string]any{"type": "INTEGER", "description": "Ko'pi bilan nechta, 1–50, standart 30"},
	}, "what")}
}

func endOfToday() time.Time {
	n := time.Now().In(uzLoc)
	return time.Date(n.Year(), n.Month(), n.Day(), 23, 59, 59, 0, uzLoc)
}

// ---- amoCRM / Kommo ----

// AmoCRM — amoCRM (yoki Kommo) uzoq muddatli tokeni orqali: lidlar va vazifalar.
type AmoCRM struct {
	HTTP    *http.Client
	BaseURL string // test uchun; bo'sh — https://<subdomain>.<zona>
}

var amoSubRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

func (a *AmoCRM) Info() Info {
	return Info{ID: "amocrm", Name: "amoCRM", Icon: "💼", Category: "Biznes",
		Description: "Yangi lidlar va bugungi vazifalar: «bugun kimga qo'ng'iroq qilish kerak», «kechagi lidlar qancha». Faqat o'qish.",
		Fields: []Field{
			{Key: "subdomain", Label: "Akkaunt manzili", Type: FieldText, Required: true, Placeholder: "kompaniya",
				Help: "amoCRM manzilingizning birinchi qismi: kompaniya.amocrm.ru → «kompaniya»."},
			{Key: "zone", Label: "Xizmat", Type: FieldSelect, Default: "amocrm.ru", Options: []Option{{"amocrm.ru", "amoCRM (amocrm.ru)"}, {"kommo.com", "Kommo (kommo.com)"}}},
			{Key: "token", Label: "Uzoq muddatli token", Type: FieldSecret, Required: true,
				Help: "amoCRM → amoMarket → ⋯ → «Integratsiya yaratish» (Внешняя интеграция) → «Ключи и доступы» → «Долгосрочный токен» → nusxa oling. Faqat administrator yarata oladi."},
		},
		Examples: []string{"Har ish kuni 09:00 da bugungi vazifalar va muddati o'tganlar", "Har kuni 19:00 da bugungi yangi lidlar xulosasi"}}
}

func (a *AmoCRM) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{crmTool("amocrm", "amoCRM: yangi lidlar (leads) yoki bajarilmagan vazifalar (tasks).")}
}

func (a *AmoCRM) Validate(cfg Config) error {
	if !amoSubRe.MatchString(cfg["subdomain"]) {
		return &ErrInvalid{"akkaunt manzili noto'g'ri: faqat «kompaniya» qismini yozing"}
	}
	return nil
}

func (a *AmoCRM) get(ctx context.Context, cfg Config, path string, q url.Values, out any) error {
	if err := a.Validate(cfg); err != nil || cfg["token"] == "" {
		return errors.New("amoCRM sozlanmagan (Plaginlar → amoCRM)")
	}
	zone := cfg["zone"]
	if zone != "kommo.com" {
		zone = "amocrm.ru"
	}
	base := a.BaseURL
	if base == "" {
		base = "https://" + cfg["subdomain"] + "." + zone
	}
	data, err := getBody(ctx, a.HTTP, base+path+"?"+q.Encode(), map[string]string{"Authorization": "Bearer " + cfg["token"]})
	if err != nil {
		if strings.Contains(err.Error(), "401") {
			return errors.New("amoCRM tokenni rad etdi — yangi token yarating")
		}
		return err
	}
	if len(data) == 0 {
		return nil // 204 — bo'sh ro'yxat
	}
	return json.Unmarshal(data, out)
}

func (a *AmoCRM) Call(ctx context.Context, cfg Config, _ string, args map[string]any) (string, error) {
	limit := argInt(args, "limit", 30, 1, 50)
	var b strings.Builder
	if argString(args, "what") == "tasks" {
		var out struct {
			Embedded struct {
				Tasks []struct {
					Text         string `json:"text"`
					CompleteTill int64  `json:"complete_till"`
					EntityType   string `json:"entity_type"`
				} `json:"tasks"`
			} `json:"_embedded"`
		}
		q := url.Values{"filter[is_completed]": {"0"}, "filter[complete_till][to]": {fmt.Sprint(endOfToday().Unix())}, "limit": {fmt.Sprint(limit)}}
		if err := a.get(ctx, cfg, "/api/v4/tasks", q, &out); err != nil {
			return "", err
		}
		if len(out.Embedded.Tasks) == 0 {
			return "Bugunga bajarilmagan vazifa yo'q.", nil
		}
		now := time.Now()
		for i, t := range out.Embedded.Tasks {
			due := time.Unix(t.CompleteTill, 0)
			mark := ""
			if due.Before(now) {
				mark = " ❗muddati o'tgan"
			}
			fmt.Fprintf(&b, "%d. %s — muddat %s%s\n", i+1, t.Text, due.In(uzLoc).Format("02.01 15:04"), mark)
		}
		return b.String(), nil
	}
	days := argInt(args, "days", 1, 1, 30)
	var out struct {
		Embedded struct {
			Leads []struct {
				Name      string  `json:"name"`
				Price     float64 `json:"price"`
				CreatedAt int64   `json:"created_at"`
			} `json:"leads"`
		} `json:"_embedded"`
	}
	q := url.Values{"filter[created_at][from]": {fmt.Sprint(time.Now().AddDate(0, 0, -days).Unix())}, "limit": {fmt.Sprint(limit)}, "order[created_at]": {"desc"}}
	if err := a.get(ctx, cfg, "/api/v4/leads", q, &out); err != nil {
		return "", err
	}
	if len(out.Embedded.Leads) == 0 {
		return fmt.Sprintf("Oxirgi %d kunda yangi lid yo'q.", days), nil
	}
	fmt.Fprintf(&b, "Oxirgi %d kunda %d ta yangi lid:\n", days, len(out.Embedded.Leads))
	for i, l := range out.Embedded.Leads {
		fmt.Fprintf(&b, "%d. %s — %.0f — %s\n", i+1, l.Name, l.Price, time.Unix(l.CreatedAt, 0).In(uzLoc).Format("02.01 15:04"))
	}
	return b.String(), nil
}

// ---- Bitrix24 ----

// Bitrix24 — kiruvchi vebhuk (webhook) orqali: lidlar, bitimlar va vazifalar.
type Bitrix24 struct {
	HTTP *http.Client
	// AllowHost — test uchun (httptest manzili); bo'sh — faqat *.bitrix24.* domenlar.
	AllowHost string
}

var (
	bitrixHostRe = regexp.MustCompile(`^[a-z0-9-]+\.bitrix24\.(ru|com|kz|by|de|eu|uz|ua|es|fr|it|pl|in|com\.br|com\.tr)$`)
	bitrixPathRe = regexp.MustCompile(`^/rest/\d+/[A-Za-z0-9]+/?$`)
)

func (x *Bitrix24) Info() Info {
	return Info{ID: "bitrix24", Name: "Bitrix24", Icon: "🏢", Category: "Biznes",
		Description: "Yangi lidlar, bitimlar va bugungi vazifalar xulosasi. Faqat o'qish.",
		Fields: []Field{{Key: "webhook", Label: "Vebhuk manzili", Type: FieldSecret, Required: true,
			Placeholder: "https://kompaniya.bitrix24.ru/rest/1/…/",
			Help:        "Bitrix24 → Ilovalar → Dasturchilar uchun → «Boshqa» → «Kiruvchi vebhuk» (Входящий вебхук). Ruxsatlar: CRM va Vazifalar. «Saqlash»dan keyin chiqqan manzilni nusxalab shu yerga qo'ying."}},
		Examples: []string{"Har ish kuni 09:00 da bugungi vazifalarim", "Har kuni 19:00 da bugungi yangi bitimlar va ularning summasi"}}
}

func (x *Bitrix24) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{crmTool("bitrix24", "Bitrix24: yangi lidlar (leads), bitimlar (deals) yoki bugungi vazifalar (tasks).")}
}

// base — vebhuk manzili tekshirilgan holda (faqat https://*.bitrix24.*/rest/<id>/<kalit>/ — SSRF yo'q).
func (x *Bitrix24) base(cfg Config) (string, error) {
	u, err := url.Parse(strings.TrimSpace(cfg["webhook"]))
	bad := &ErrInvalid{"vebhuk manzili noto'g'ri: https://kompaniya.bitrix24.ru/rest/1/…/ ko'rinishida bo'lsin"}
	if err != nil || u.User != nil || u.RawQuery != "" || !bitrixPathRe.MatchString(u.Path) {
		return "", bad
	}
	if !(x.AllowHost != "" && u.Host == x.AllowHost) && (u.Scheme != "https" || !bitrixHostRe.MatchString(u.Host)) {
		return "", bad
	}
	return u.Scheme + "://" + u.Host + strings.TrimSuffix(u.Path, "/") + "/", nil
}

func (x *Bitrix24) Validate(cfg Config) error {
	_, err := x.base(cfg)
	return err
}

func (x *Bitrix24) call(ctx context.Context, cfg Config, method string, q url.Values, out any) error {
	base, err := x.base(cfg)
	if err != nil {
		return errors.New("Bitrix24 sozlanmagan (Plaginlar → Bitrix24)")
	}
	data, err := getBody(ctx, x.HTTP, base+method+".json?"+q.Encode(), nil)
	if err != nil {
		if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "403") {
			return errors.New("Bitrix24 vebhukni rad etdi — ruxsatlarini (CRM, Vazifalar) tekshiring")
		}
		return err
	}
	return json.Unmarshal(data, out)
}

func (x *Bitrix24) Call(ctx context.Context, cfg Config, _ string, args map[string]any) (string, error) {
	limit := argInt(args, "limit", 30, 1, 50)
	var b strings.Builder
	switch argString(args, "what") {
	case "tasks":
		var out struct {
			Result struct {
				Tasks []struct {
					Title    string `json:"title"`
					Deadline string `json:"deadline"`
				} `json:"tasks"`
			} `json:"result"`
		}
		q := url.Values{"filter[<=DEADLINE]": {endOfToday().Format(time.RFC3339)}, "filter[!REAL_STATUS]": {"5"},
			"select[]": {"TITLE", "DEADLINE"}, "order[DEADLINE]": {"asc"}}
		if err := x.call(ctx, cfg, "tasks.task.list", q, &out); err != nil {
			return "", err
		}
		if len(out.Result.Tasks) == 0 {
			return "Bugunga bajarilmagan vazifa yo'q.", nil
		}
		now := time.Now()
		for i, t := range out.Result.Tasks {
			if i >= limit {
				break
			}
			mark := ""
			if d, err := time.Parse(time.RFC3339, t.Deadline); err == nil && d.Before(now) {
				mark = " ❗muddati o'tgan"
			}
			fmt.Fprintf(&b, "%d. %s — muddat %s%s\n", i+1, t.Title, t.Deadline, mark)
		}
		return b.String(), nil
	case "leads":
		return x.list(ctx, cfg, "crm.lead.list", "lid", args, limit)
	default:
		return x.list(ctx, cfg, "crm.deal.list", "bitim", args, limit)
	}
}

func (x *Bitrix24) list(ctx context.Context, cfg Config, method, what string, args map[string]any, limit int) (string, error) {
	days := argInt(args, "days", 1, 1, 30)
	var out struct {
		Result []struct {
			Title       string `json:"TITLE"`
			Opportunity string `json:"OPPORTUNITY"`
			Currency    string `json:"CURRENCY_ID"`
			Created     string `json:"DATE_CREATE"`
		} `json:"result"`
	}
	q := url.Values{"filter[>DATE_CREATE]": {time.Now().AddDate(0, 0, -days).Format(time.RFC3339)},
		"select[]": {"TITLE", "OPPORTUNITY", "CURRENCY_ID", "DATE_CREATE"}, "order[DATE_CREATE]": {"DESC"}}
	if err := x.call(ctx, cfg, method, q, &out); err != nil {
		return "", err
	}
	if len(out.Result) == 0 {
		return fmt.Sprintf("Oxirgi %d kunda yangi %s yo'q.", days, what), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Oxirgi %d kunda %d ta yangi %s:\n", days, len(out.Result), what)
	for i, r := range out.Result {
		if i >= limit {
			break
		}
		fmt.Fprintf(&b, "%d. %s — %s %s — %s\n", i+1, r.Title, r.Opportunity, r.Currency, r.Created)
	}
	return b.String(), nil
}
