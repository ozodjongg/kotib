// Package plugin: kotibga ulanadigan tashqi xizmatlar (connector'lar) — internet qidiruvi,
// YouTube, ob-havo, valyuta kursi va kelajakda qo'shiladiganlar. Har plagin AI'ga bir yoki bir
// nechta vosita (function calling) beradi; mijoz Mini App'da plaginni yoqadi va sozlaydi.
// Rejalashtirilgan vazifalar (internal/schedule) yoqilgan plaginlar vositalaridan foydalanadi.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
)

// FieldType — sozlama maydoni turi.
type FieldType string

const (
	FieldText   FieldType = "text"
	FieldSecret FieldType = "secret" // API kalit: Mini App'ga qaytarilmaydi, bazada shifrlangan
	FieldSelect FieldType = "select"
)

// Option — select maydon varianti.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Field — plagin sozlamasi.
type Field struct {
	Key         string    `json:"key"`
	Label       string    `json:"label"`
	Type        FieldType `json:"type"`
	Placeholder string    `json:"placeholder,omitempty"`
	Help        string    `json:"help,omitempty"`
	Required    bool      `json:"required,omitempty"`
	Options     []Option  `json:"options,omitempty"`
	Default     string    `json:"default,omitempty"`
}

// Info — katalogdagi plagin tavsifi.
type Info struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Icon        string   `json:"icon"` // emoji
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Soon        bool     `json:"soon,omitempty"` // tez orada: hali yoqib bo'lmaydi
	Fields      []Field  `json:"fields,omitempty"`
	Tools       []string `json:"tools,omitempty"` // AI'ga beriladigan vositalar nomi (ko'rsatish uchun)
	Examples    []string `json:"examples,omitempty"`
}

// Config — mijozning plagin sozlamalari (maydon kaliti → qiymat).
type Config map[string]string

// Plugin — bitta connector.
type Plugin interface {
	Info() Info
	// Tools — AI'ga beriladigan vositalar (nomlar butun reestrda noyob bo'lishi shart).
	Tools() []llm.ToolDecl
	// Call vositani bajaradi; natija — model uchun matn.
	Call(ctx context.Context, cfg Config, tool string, args map[string]any) (string, error)
}

// Registry — mavjud plaginlar.
type Registry struct {
	list  []Plugin
	byID  map[string]Plugin
	tools map[string]Plugin
}

// NewRegistry plaginlarni ro'yxatga oladi (ID va vosita nomlari takrorlanmasligi shart).
func NewRegistry(ps ...Plugin) (*Registry, error) {
	r := &Registry{byID: map[string]Plugin{}, tools: map[string]Plugin{}}
	for _, p := range ps {
		id := p.Info().ID
		if id == "" || r.byID[id] != nil {
			return nil, fmt.Errorf("plagin ID noto'g'ri yoki takror: %q", id)
		}
		for _, t := range p.Tools() {
			if r.tools[t.Name] != nil {
				return nil, fmt.Errorf("vosita takror: %q", t.Name)
			}
			r.tools[t.Name] = p
		}
		r.byID[id] = p
		r.list = append(r.list, p)
	}
	return r, nil
}

// Catalog — barcha plaginlar (avval mavjudlari, keyin "tez orada").
func (r *Registry) Catalog() []Info {
	out := make([]Info, 0, len(r.list))
	for _, p := range r.list {
		info := p.Info()
		for _, t := range p.Tools() {
			info.Tools = append(info.Tools, t.Name)
		}
		out = append(out, info)
	}
	sort.SliceStable(out, func(i, j int) bool { return !out[i].Soon && out[j].Soon })
	return out
}

func (r *Registry) Get(id string) (Plugin, bool) {
	p, ok := r.byID[id]
	return p, ok
}

const (
	maxFieldRunes = 200
	maxFields     = 10
)

// ErrInvalid — sozlama xatosi (matni mijozga ko'rsatiladi).
type ErrInvalid struct{ Msg string }

func (e *ErrInvalid) Error() string { return e.Msg }

// Clean sozlamani tekshiradi: faqat ma'lum maydonlar, majburiylari bor, select qiymati ro'yxatda.
// old — avvalgi sozlama: secret maydon bo'sh yuborilsa eski qiymat saqlanadi (kalit qayta
// ko'rsatilmaydi, shuning uchun Mini App uni bo'sh yuboradi).
func Clean(info Info, in, old Config) (Config, error) {
	if info.Soon {
		return nil, &ErrInvalid{"bu plagin hali mavjud emas"}
	}
	out := Config{}
	for _, f := range info.Fields {
		v := strings.TrimSpace(in[f.Key])
		if utf8.RuneCountInString(v) > maxFieldRunes || strings.ContainsAny(v, "\r\n\x00") {
			return nil, &ErrInvalid{f.Label + ": qiymat noto'g'ri"}
		}
		if v == "" && f.Type == FieldSecret {
			v = old[f.Key]
		}
		if v == "" {
			v = f.Default
		}
		if f.Type == FieldSelect && v != "" {
			ok := false
			for _, o := range f.Options {
				ok = ok || o.Value == v
			}
			if !ok {
				return nil, &ErrInvalid{f.Label + ": ro'yxatdan tanlang"}
			}
		}
		if v == "" && f.Required {
			return nil, &ErrInvalid{f.Label + " kerak"}
		}
		if v != "" {
			out[f.Key] = v
		}
	}
	return out, nil
}

// Public — Mini App'ga qaytariladigan sozlama: secret maydonlar o'rniga faqat oxirgi 4 belgi.
func Public(info Info, cfg Config) map[string]string {
	out := map[string]string{}
	for _, f := range info.Fields {
		v, ok := cfg[f.Key]
		if !ok {
			continue
		}
		if f.Type == FieldSecret {
			tail := ""
			if r := []rune(v); len(r) > 8 {
				tail = string(r[len(r)-4:])
			}
			out[f.Key] = "••••" + tail
			continue
		}
		out[f.Key] = v
	}
	return out
}

// Enabled — mijozda yoqilgan plagin va uning sozlamasi.
type Enabled struct {
	ID     string
	Config Config
}

// Toolset yoqilgan plaginlar vositalari va ularni chaqiruvchi funksiya (rejalashtirilgan vazifa uchun).
func (r *Registry) Toolset(enabled []Enabled) ([]llm.ToolDecl, llm.ToolCaller) {
	cfgs := map[string]Config{}
	var decls []llm.ToolDecl
	for _, e := range enabled {
		p, ok := r.byID[e.ID]
		if !ok || p.Info().Soon {
			continue
		}
		cfgs[e.ID] = e.Config
		decls = append(decls, p.Tools()...)
	}
	call := func(ctx context.Context, name string, args map[string]any) (string, error) {
		p, ok := r.tools[name]
		if !ok {
			return "", fmt.Errorf("noma'lum vosita: %s", name)
		}
		cfg, ok := cfgs[p.Info().ID]
		if !ok {
			return "", fmt.Errorf("plagin yoqilmagan: %s", p.Info().ID)
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return p.Call(cctx, cfg, name, args)
	}
	return decls, call
}

// ---- yordamchilar ----

func argString(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func argInt(args map[string]any, key string, def, lo, hi int) int {
	n := def
	switch v := args[key].(type) {
	case float64:
		n = int(v)
	case int:
		n = v
	}
	return min(max(n, lo), hi)
}

// getBody — tashqi API'dan GET (javob 1 MB bilan cheklangan).
func getBody(ctx context.Context, c *http.Client, url string, hdr map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, errors.New("xizmatga ulanib bo'lmadi")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return data, fmt.Errorf("xizmat javobi: %d", resp.StatusCode)
	}
	return data, nil
}
