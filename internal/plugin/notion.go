package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
)

// Notion — ichki integratsiya tokeni orqali: sahifa va bazalarni qidirish, o'qish va egasi
// ko'rsatgan bitta sahifaga yozish. Integratsiya faqat egasi ulagan (Connections) sahifalarni ko'radi.
type Notion struct {
	HTTP    *http.Client
	BaseURL string // default https://api.notion.com/v1
}

// notionVersion — Notion API versiyasi (sarlavha).
const notionVersion = "2022-06-28"

// maxNotionAppend — bitta yozuvda ko'pi bilan shuncha belgi.
const maxNotionAppend = 4000

func (n *Notion) Info() Info {
	return Info{ID: "notion", Name: "Notion", Icon: "🗒", Category: "Hujjatlar",
		Description: "Notion sahifa va bazalaringizni qidiradi va o'qiydi (vazifalar, eslatmalar, rejalar). Yozish faqat siz ko'rsatgan bitta sahifaga — qolganlari o'zgartirilmaydi.",
		Fields: []Field{
			{Key: "token", Label: "Integratsiya tokeni", Type: FieldSecret, Required: true, Placeholder: "ntn_…",
				Help: "notion.so/profile/integrations → «New integration» (Internal) → «Internal Integration Secret»ni nusxalang. " +
					"So'ng kerakli sahifada ⋯ → «Connections» → integratsiyangizni qo'shing — kotib faqat shu sahifalarni ko'radi."},
			{Key: "page", Label: "Yozish uchun sahifa (ixtiyoriy)", Type: FieldText, Placeholder: "https://www.notion.so/…",
				Help: "Kotib natijalarni faqat shu sahifa oxiriga qo'shadi. Bo'sh qoldirsangiz — faqat o'qish."},
		},
		Examples: []string{"Har ish kuni 09:00 da Notion'dagi «Vazifalar» bazasidan bugungi ishlar", "Har kuni 21:00 da kun yangiliklari xulosasini Notion sahifamga yoz"}}
}

func (n *Notion) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{
		{Name: "notion_search", Description: "Egasining Notion'ida sahifa va bazalarni nomi bo'yicha qidiradi; so'rov bo'lmasa — oxirgi tahrirlanganlar. ID qaytadi (notion_read uchun).",
			Params: obj(map[string]any{
				"query":       str("Qidiruv so'zi (ixtiyoriy)"),
				"max_results": map[string]any{"type": "INTEGER", "description": "1–20, standart 10"},
			})},
		{Name: "notion_read", Description: "Notion sahifasi matnini yoki baza (database) qatorlarini o'qiydi.",
			Params: obj(map[string]any{"id": str("Sahifa yoki baza ID (notion_search natijasidan)")}, "id")},
		{Name: "notion_append", Description: "Matnni egasi sozlamada ko'rsatgan Notion sahifasi oxiriga qo'shadi (boshqa sahifaga yozib bo'lmaydi).",
			Params: obj(map[string]any{"text": str("Qo'shiladigan matn (4000 belgigacha)")}, "text")},
	}
}

var (
	notionTokenRe = regexp.MustCompile(`^(ntn_|secret_)[A-Za-z0-9]{20,100}$`)
	notionIDRe    = regexp.MustCompile(`[0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12}`)
)

// notionID — havola yoki ID'dan 32 belgili Notion ID ("" — topilmadi). Havolada ID oxirida turadi.
func notionID(s string) string {
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	m := notionIDRe.FindAllString(s, -1)
	if len(m) == 0 {
		return ""
	}
	return strings.ToLower(strings.ReplaceAll(m[len(m)-1], "-", ""))
}

func (n *Notion) Validate(cfg Config) error {
	if !notionTokenRe.MatchString(cfg["token"]) {
		return &ErrInvalid{"Notion tokeni noto'g'ri: «ntn_» bilan boshlanadigan Internal Integration Secret'ni kiriting"}
	}
	if cfg["page"] != "" && notionID(cfg["page"]) == "" {
		return &ErrInvalid{"sahifa havolasi noto'g'ri: Notion'da sahifani oching, ⋯ → «Copy link»"}
	}
	return nil
}

func (n *Notion) base() string {
	if n.BaseURL != "" {
		return strings.TrimRight(n.BaseURL, "/")
	}
	return "https://api.notion.com/v1"
}

// errNotionNotFound — sahifa yo'q yoki integratsiyaga ulanmagan (Notion ikkalasiga ham 404 beradi).
var errNotionNotFound = errors.New("Notion: topilmadi — sahifada ⋯ → «Connections» orqali integratsiyani qo'shing")

// do — Notion API so'rovi (javob 1 MB bilan cheklangan).
func (n *Notion) do(ctx context.Context, cfg Config, method, path string, body, out any) error {
	if cfg["token"] == "" {
		return errors.New("Notion sozlanmagan (Plaginlar → Notion)")
	}
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, n.base()+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg["token"])
	req.Header.Set("Notion-Version", notionVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c := n.HTTP
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return errors.New("Notion'ga ulanib bo'lmadi")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return errors.New("Notion tokenni rad etdi — integratsiya tokenini qayta kiriting")
	case http.StatusNotFound:
		return errNotionNotFound
	case http.StatusTooManyRequests:
		return errors.New("Notion: so'rovlar ko'p, birozdan keyin urinib ko'ring")
	default:
		return fmt.Errorf("Notion javobi: %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("Notion javobini o'qib bo'lmadi")
	}
	return nil
}

type notionText struct {
	PlainText string `json:"plain_text"`
}

func joinText(ts []notionText) string {
	var b strings.Builder
	for _, t := range ts {
		b.WriteString(t.PlainText)
	}
	return strings.TrimSpace(b.String())
}

// notionProp — sahifa/baza qatori xususiyati (faqat matnga aylantiriladigan turlari).
type notionProp struct {
	Type     string       `json:"type"`
	Title    []notionText `json:"title"`
	RichText []notionText `json:"rich_text"`
	Select   *struct {
		Name string `json:"name"`
	} `json:"select"`
	Status *struct {
		Name string `json:"name"`
	} `json:"status"`
	MultiSelect []struct {
		Name string `json:"name"`
	} `json:"multi_select"`
	Date *struct {
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"date"`
	Checkbox *bool    `json:"checkbox"`
	Number   *float64 `json:"number"`
	URL      *string  `json:"url"`
}

func (p notionProp) String() string {
	switch p.Type {
	case "title":
		return joinText(p.Title)
	case "rich_text":
		return joinText(p.RichText)
	case "select":
		if p.Select != nil {
			return p.Select.Name
		}
	case "status":
		if p.Status != nil {
			return p.Status.Name
		}
	case "multi_select":
		var names []string
		for _, s := range p.MultiSelect {
			names = append(names, s.Name)
		}
		return strings.Join(names, ", ")
	case "date":
		if p.Date != nil {
			if p.Date.End != "" {
				return p.Date.Start + " – " + p.Date.End
			}
			return p.Date.Start
		}
	case "checkbox":
		if p.Checkbox != nil {
			if *p.Checkbox {
				return "✅"
			}
			return "☐"
		}
	case "number":
		if p.Number != nil {
			return fmt.Sprint(*p.Number)
		}
	case "url":
		if p.URL != nil {
			return *p.URL
		}
	}
	return ""
}

// notionObject — qidiruv natijasi, sahifa yoki baza qatori.
type notionObject struct {
	Object         string                `json:"object"`
	ID             string                `json:"id"`
	URL            string                `json:"url"`
	LastEditedTime string                `json:"last_edited_time"`
	Title          []notionText          `json:"title"` // baza nomi
	Properties     map[string]notionProp `json:"properties"`
}

func (o notionObject) title() string {
	if t := joinText(o.Title); t != "" {
		return t
	}
	for _, p := range o.Properties {
		if p.Type == "title" {
			if t := joinText(p.Title); t != "" {
				return t
			}
		}
	}
	return "(nomsiz)"
}

func (n *Notion) Call(ctx context.Context, cfg Config, tool string, args map[string]any) (string, error) {
	switch tool {
	case "notion_read":
		return n.read(ctx, cfg, argString(args, "id"))
	case "notion_append":
		return n.append(ctx, cfg, argString(args, "text"))
	}
	return n.search(ctx, cfg, args)
}

func (n *Notion) search(ctx context.Context, cfg Config, args map[string]any) (string, error) {
	body := map[string]any{"page_size": argInt(args, "max_results", 10, 1, 20),
		"sort": map[string]string{"direction": "descending", "timestamp": "last_edited_time"}}
	if q := argString(args, "query"); q != "" {
		body["query"] = q
	}
	var out struct {
		Results []notionObject `json:"results"`
	}
	if err := n.do(ctx, cfg, http.MethodPost, "/search", body, &out); err != nil {
		return "", err
	}
	if len(out.Results) == 0 {
		return "Notion'da mos sahifa topilmadi (kotib faqat integratsiyaga ulangan sahifalarni ko'radi).", nil
	}
	var b strings.Builder
	for i, o := range out.Results {
		kind := "sahifa"
		if o.Object == "database" {
			kind = "baza"
		}
		when := ""
		if t, err := time.Parse(time.RFC3339, o.LastEditedTime); err == nil {
			when = ", tahrirlangan " + t.In(uzLoc).Format("02.01.2006 15:04")
		}
		fmt.Fprintf(&b, "%d. %s (%s%s) — ID: %s — %s\n", i+1, o.title(), kind, when, strings.ReplaceAll(o.ID, "-", ""), o.URL)
	}
	return b.String(), nil
}

func (n *Notion) read(ctx context.Context, cfg Config, raw string) (string, error) {
	id := notionID(raw)
	if id == "" {
		return "", errors.New("Notion ID noto'g'ri (avval notion_search bilan toping)")
	}
	var page notionObject
	err := n.do(ctx, cfg, http.MethodGet, "/pages/"+id, nil, &page)
	if errors.Is(err, errNotionNotFound) {
		return n.queryDatabase(ctx, cfg, id) // ID baza bo'lishi mumkin
	}
	if err != nil {
		return "", err
	}
	var out struct {
		Results []map[string]json.RawMessage `json:"results"`
	}
	if err := n.do(ctx, cfg, http.MethodGet, "/blocks/"+id+"/children?page_size=100", nil, &out); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Sahifa «%s» (%s):\n", page.title(), page.URL)
	for _, blk := range out.Results {
		if line := notionBlockText(blk); line != "" {
			b.WriteString(line + "\n")
		}
		if b.Len() > maxFeedOutput {
			b.WriteString("…(qisqartirildi)")
			break
		}
	}
	return b.String(), nil
}

// notionBlockText — blokni bir qator matnga aylantiradi (matnsiz bloklar — "").
func notionBlockText(blk map[string]json.RawMessage) string {
	var typ string
	if json.Unmarshal(blk["type"], &typ) != nil || typ == "" {
		return ""
	}
	var c struct {
		RichText []notionText `json:"rich_text"`
		Checked  bool         `json:"checked"`
		Title    string       `json:"title"`
	}
	if json.Unmarshal(blk[typ], &c) != nil {
		return ""
	}
	text := joinText(c.RichText)
	switch typ {
	case "child_page", "child_database":
		return "📄 " + c.Title
	case "heading_1":
		return "# " + text
	case "heading_2":
		return "## " + text
	case "heading_3":
		return "### " + text
	case "bulleted_list_item", "numbered_list_item":
		return "• " + text
	case "to_do":
		if c.Checked {
			return "☑ " + text
		}
		return "☐ " + text
	case "quote", "callout":
		return "> " + text
	}
	return text
}

func (n *Notion) queryDatabase(ctx context.Context, cfg Config, id string) (string, error) {
	var db notionObject
	if err := n.do(ctx, cfg, http.MethodGet, "/databases/"+id, nil, &db); err != nil {
		return "", err
	}
	var out struct {
		Results []notionObject `json:"results"`
	}
	if err := n.do(ctx, cfg, http.MethodPost, "/databases/"+id+"/query", map[string]any{"page_size": 50}, &out); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Baza «%s» — %d qator:\n", db.title(), len(out.Results))
	for i, row := range out.Results {
		var parts []string
		for name, p := range row.Properties {
			if p.Type == "title" {
				continue
			}
			if v := p.String(); v != "" {
				parts = append(parts, name+": "+v)
			}
		}
		sort.Strings(parts) // xarita tartibi tasodifiy
		line := fmt.Sprintf("%d. %s", i+1, row.title())
		if len(parts) > 0 {
			line += " — " + strings.Join(parts, "; ")
		}
		b.WriteString(line + "\n")
		if b.Len() > maxFeedOutput {
			b.WriteString("…(qisqartirildi)")
			break
		}
	}
	return b.String(), nil
}

func (n *Notion) append(ctx context.Context, cfg Config, text string) (string, error) {
	page := notionID(cfg["page"])
	if page == "" {
		return "", errors.New("yozish uchun sahifa sozlanmagan (Plaginlar → Notion → «Yozish uchun sahifa»)")
	}
	text = strings.TrimSpace(strings.ToValidUTF8(text, ""))
	if text == "" {
		return "", errors.New("matn bo'sh")
	}
	if utf8.RuneCountInString(text) > maxNotionAppend {
		text = string([]rune(text)[:maxNotionAppend]) + "…"
	}
	var children []map[string]any
	for _, line := range strings.Split(text, "\n") {
		for r := []rune(line); len(children) < 100; { // Notion: so'rovda 100 blok, matn bo'lagi 2000 belgi
			chunk := r[:min(len(r), 2000)]
			children = append(children, map[string]any{"object": "block", "type": "paragraph",
				"paragraph": map[string]any{"rich_text": []map[string]any{{"type": "text", "text": map[string]string{"content": string(chunk)}}}}})
			if r = r[len(chunk):]; len(r) == 0 {
				break
			}
		}
	}
	if err := n.do(ctx, cfg, http.MethodPatch, "/blocks/"+page+"/children", map[string]any{"children": children}, nil); err != nil {
		return "", err
	}
	return "Notion sahifasiga yozildi.", nil
}
