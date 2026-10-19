package plugin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
)

// Drive — Google Drive fayllarini qidirish va o'qish (Google akkaunt orqali, faqat o'qish ruxsati):
// hujjatlar, jadvallar va taqdimotlar matni — AI xulosa va hisobot tayyorlaydi.
type Drive struct {
	Token   func(ctx context.Context, refreshToken string) (string, error)
	HTTP    *http.Client
	BaseURL string // default https://www.googleapis.com/drive/v3
}

func drivePlugin(o Options) Plugin {
	if o.DriveToken == nil {
		return soon("drive", "Google Drive", "📁", "Hujjatlar", "Fayllarni qidirish va hujjatlar xulosasi.")
	}
	return &Drive{Token: o.DriveToken, HTTP: o.HTTP, BaseURL: o.DriveURL}
}

func (d *Drive) Info() Info {
	return Info{ID: "drive", Name: "Google Drive", Icon: "📁", Category: "Hujjatlar", Connect: "google",
		Description: "Google Drive'dagi fayllaringizni qidiradi va hujjat, jadval, taqdimot matnini o'qib xulosa qiladi. Faqat o'qish — Drive'da hech narsa o'zgartirilmaydi.",
		Examples:    []string{"Har juma 18:00 da shu hafta o'zgargan hujjatlar ro'yxati", "Har kuni 09:00 da «Kun rejasi» hujjatidan qisqa xulosa"}}
}

func (d *Drive) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{
		{Name: "drive_search", Description: "Egasining Google Drive'ida fayl qidiradi (nomi yoki ichidagi matn bo'yicha); so'rov bo'lmasa — oxirgi o'zgarganlar. Har fayl uchun ID qaytadi (drive_read uchun).",
			Params: obj(map[string]any{
				"query":       str("Qidiruv so'zi (ixtiyoriy)"),
				"days":        map[string]any{"type": "INTEGER", "description": "Faqat oxirgi N kunda o'zgarganlar, 0–365 (ixtiyoriy)"},
				"max_results": map[string]any{"type": "INTEGER", "description": "1–20, standart 10"},
			})},
		{Name: "drive_read", Description: "Drive faylining matnini o'qiydi: Google Docs, Sheets (CSV), Slides va matn fayllari.",
			Params: obj(map[string]any{"file_id": str("Fayl ID (drive_search natijasidan)")}, "file_id")},
	}
}

func (d *Drive) base() string {
	if d.BaseURL != "" {
		return strings.TrimRight(d.BaseURL, "/")
	}
	return "https://www.googleapis.com/drive/v3"
}

// driveQuote — Drive qidiruv so'rovi ichidagi satr ('...') uchun ekranlash.
func driveQuote(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
}

var driveIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{10,200}$`)

// driveKinds — Google fayl turlarining odamga tushunarli nomi.
var driveKinds = map[string]string{
	"application/vnd.google-apps.document":     "hujjat",
	"application/vnd.google-apps.spreadsheet":  "jadval",
	"application/vnd.google-apps.presentation": "taqdimot",
	"application/vnd.google-apps.folder":       "papka",
	"application/pdf":                          "PDF",
}

// driveExport — Google formatidagi fayl qaysi matn ko'rinishida eksport qilinadi.
var driveExport = map[string]string{
	"application/vnd.google-apps.document":     "text/plain",
	"application/vnd.google-apps.spreadsheet":  "text/csv",
	"application/vnd.google-apps.presentation": "text/plain",
}

func (d *Drive) Call(ctx context.Context, cfg Config, tool string, args map[string]any) (string, error) {
	if tool == "drive_read" {
		return d.read(ctx, cfg, argString(args, "file_id"))
	}
	return d.search(ctx, cfg, args)
}

func (d *Drive) search(ctx context.Context, cfg Config, args map[string]any) (string, error) {
	q := []string{"trashed = false"}
	query := argString(args, "query")
	if query != "" {
		if utf8.RuneCountInString(query) > 100 {
			return "", errors.New("qidiruv so'zi juda uzun")
		}
		e := driveQuote(query)
		q = append(q, fmt.Sprintf("(name contains '%s' or fullText contains '%s')", e, e))
	}
	if days := argInt(args, "days", 0, 0, 365); days > 0 {
		q = append(q, fmt.Sprintf("modifiedTime > '%s'", time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)))
	}
	v := url.Values{"q": {strings.Join(q, " and ")}, "pageSize": {fmt.Sprint(argInt(args, "max_results", 10, 1, 20))},
		"fields": {"files(id,name,mimeType,modifiedTime,webViewLink)"}}
	if query == "" {
		v.Set("orderBy", "modifiedTime desc") // fullText qidiruvi bilan saralash qo'llanmaydi
	}
	var out struct {
		Files []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			MimeType     string `json:"mimeType"`
			ModifiedTime string `json:"modifiedTime"`
			WebViewLink  string `json:"webViewLink"`
		} `json:"files"`
	}
	if err := oauthGet(ctx, d.HTTP, d.Token, cfg, "Google Drive", d.base()+"/files?"+v.Encode(), &out); err != nil {
		return "", err
	}
	if len(out.Files) == 0 {
		return "Drive'da mos fayl topilmadi.", nil
	}
	var b strings.Builder
	for i, f := range out.Files {
		kind := driveKinds[f.MimeType]
		if kind == "" {
			kind = "fayl"
		}
		when := ""
		if t, err := time.Parse(time.RFC3339, f.ModifiedTime); err == nil {
			when = ", o'zgargan " + t.In(uzLoc).Format("02.01.2006 15:04")
		}
		fmt.Fprintf(&b, "%d. %s (%s%s) — ID: %s", i+1, f.Name, kind, when, f.ID)
		if f.WebViewLink != "" {
			b.WriteString(" — " + f.WebViewLink)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

func (d *Drive) read(ctx context.Context, cfg Config, id string) (string, error) {
	if !driveIDRe.MatchString(id) {
		return "", errors.New("fayl ID noto'g'ri (avval drive_search bilan toping)")
	}
	var meta struct {
		Name     string `json:"name"`
		MimeType string `json:"mimeType"`
		Size     string `json:"size"`
	}
	base := d.base() + "/files/" + url.PathEscape(id)
	if err := oauthGet(ctx, d.HTTP, d.Token, cfg, "Google Drive", base+"?fields=name,mimeType,size", &meta); err != nil {
		return "", err
	}
	var u string
	switch {
	case driveExport[meta.MimeType] != "":
		u = base + "/export?" + url.Values{"mimeType": {driveExport[meta.MimeType]}}.Encode()
	case strings.HasPrefix(meta.MimeType, "text/") || meta.MimeType == "application/json":
		u = base + "?alt=media"
	default:
		return fmt.Sprintf("«%s» faylini o'qib bo'lmaydi: faqat Google Docs, Sheets, Slides va matn fayllari.", meta.Name), nil
	}
	data, err := oauthRaw(ctx, d.HTTP, d.Token, cfg, "Google Drive", u)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(strings.TrimPrefix(strings.ToValidUTF8(string(data), ""), "\ufeff")) // eksportda BOM bo'ladi
	if text == "" {
		return fmt.Sprintf("«%s» bo'sh.", meta.Name), nil
	}
	if len(text) > maxFeedOutput {
		text = strings.ToValidUTF8(text[:maxFeedOutput], "") + "\n…(qisqartirildi)"
	}
	return fmt.Sprintf("Fayl «%s»:\n%s", meta.Name, text), nil
}
