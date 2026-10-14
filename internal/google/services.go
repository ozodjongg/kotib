package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// ---- Calendar ----

type busyCache struct {
	at   time.Time
	list []llm.Interval
}

// Busy asosiy taqvimdagi band vaqtlar [from, to). 2 daqiqa keshlanadi (har xabarga
// Google'ga so'rov yubormaslik uchun).
func (c *Client) Busy(ctx context.Context, owner int64, from, to time.Time) ([]llm.Interval, error) {
	c.mu.Lock()
	c.init()
	bc, ok := c.busy[owner]
	c.mu.Unlock()
	if ok && c.now().Sub(bc.at) < busyTTL {
		return bc.list, nil
	}
	var out struct {
		Calendars map[string]struct {
			Busy []struct {
				Start time.Time `json:"start"`
				End   time.Time `json:"end"`
			} `json:"busy"`
		} `json:"calendars"`
	}
	err := c.do(ctx, owner, http.MethodPost, c.Endpoints.Calendar+"/freeBusy", map[string]any{
		"timeMin": from.UTC().Format(time.RFC3339),
		"timeMax": to.UTC().Format(time.RFC3339),
		"items":   []map[string]string{{"id": "primary"}},
	}, &out)
	if err != nil {
		return nil, err
	}
	var list []llm.Interval
	for _, b := range out.Calendars["primary"].Busy {
		list = append(list, llm.Interval{Start: b.Start, End: b.End})
	}
	c.mu.Lock()
	if len(c.busy) < maxCacheItems {
		c.busy[owner] = busyCache{at: c.now(), list: list}
	}
	c.mu.Unlock()
	return list, nil
}

// CreateEvent asosiy taqvimga tadbir qo'shadi va uning havolasini qaytaradi.
func (c *Client) CreateEvent(ctx context.Context, owner int64, e llm.Event) (string, error) {
	tz := e.Start.Location().String()
	if tz == "Local" || tz == "" {
		tz = "UTC"
	}
	var out struct {
		HTMLLink string `json:"htmlLink"`
	}
	err := c.do(ctx, owner, http.MethodPost, c.Endpoints.Calendar+"/calendars/primary/events", map[string]any{
		"summary":     e.Title,
		"description": "Telegram kotibi taklif qildi, siz tasdiqladingiz.",
		"start":       map[string]string{"dateTime": e.Start.Format(time.RFC3339), "timeZone": tz},
		"end":         map[string]string{"dateTime": e.Start.Add(e.Duration).Format(time.RFC3339), "timeZone": tz},
	}, &out)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	delete(c.busy, owner) // band vaqtlar o'zgardi
	c.mu.Unlock()
	return out.HTMLLink, nil
}

// ---- Tasks ----

// CreateTask asosiy ro'yxatga vazifa qo'shadi.
func (c *Client) CreateTask(ctx context.Context, owner int64, t llm.Task) error {
	body := map[string]any{"title": t.Title, "notes": "Telegram kotibi taklif qildi."}
	if !t.Due.IsZero() {
		// Tasks API faqat sanani saqlaydi (vaqt e'tiborsiz qoldiriladi).
		y, m, d := t.Due.Date()
		body["due"] = time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	}
	return c.do(ctx, owner, http.MethodPost, c.Endpoints.Tasks+"/lists/@default/tasks", body, nil)
}

// ---- Contacts ----

type contactKey struct {
	owner, user int64
}

type contactCache struct {
	at         time.Time
	name, note string
}

type person struct {
	Names []struct {
		DisplayName string `json:"displayName"`
	} `json:"names"`
	Biographies []struct {
		Value string `json:"value"`
	} `json:"biographies"`
	PhoneNumbers []struct {
		Value         string `json:"value"`
		CanonicalForm string `json:"canonicalForm"`
	} `json:"phoneNumbers"`
}

// Contact Google Contacts'dan Telegram kontaktiga mos yozuvni topadi: avval telefon,
// bo'lmasa ism bo'yicha (faqat yagona aniq moslik). Xatolar jim — kontekst ixtiyoriy.
func (c *Client) Contact(ctx context.Context, owner, userID int64, name, phone string) (string, string) {
	key := contactKey{owner, userID}
	c.mu.Lock()
	c.init()
	cc, ok := c.contacts[key]
	warmed := c.warmed[owner]
	c.mu.Unlock()
	if ok && c.now().Sub(cc.at) < contactTTL {
		return cc.name, cc.note
	}
	if !warmed {
		// People API qidiruv keshini isitish talab qilinadi (bo'sh so'rov).
		_ = c.search(ctx, owner, "", nil)
		c.mu.Lock()
		c.warmed[owner] = true
		c.mu.Unlock()
	}
	var found *person
	if digits := onlyDigits(phone); len(digits) >= 7 {
		var res []person
		if err := c.search(ctx, owner, "+"+digits, &res); err == nil {
			for i := range res {
				if res[i].hasPhone(digits) {
					found = &res[i]
					break
				}
			}
		}
	}
	if found == nil && strings.TrimSpace(name) != "" {
		var res []person
		if err := c.search(ctx, owner, strings.TrimSpace(name), &res); err == nil && len(res) == 1 {
			found = &res[0]
		}
	}
	cc = contactCache{at: c.now()}
	if found != nil {
		if len(found.Names) > 0 {
			cc.name = found.Names[0].DisplayName
		}
		if len(found.Biographies) > 0 {
			cc.note = found.Biographies[0].Value
		}
	}
	c.mu.Lock()
	if len(c.contacts) < maxCacheItems {
		c.contacts[key] = cc
	}
	c.mu.Unlock()
	return cc.name, cc.note
}

func (c *Client) search(ctx context.Context, owner int64, query string, out *[]person) error {
	q := url.Values{"query": {query}, "readMask": {"names,biographies,phoneNumbers"}, "pageSize": {"10"}}
	var res struct {
		Results []struct {
			Person person `json:"person"`
		} `json:"results"`
	}
	if err := c.do(ctx, owner, http.MethodGet, c.Endpoints.People+"/people:searchContacts?"+q.Encode(), nil, &res); err != nil {
		return err
	}
	if out != nil {
		for _, r := range res.Results {
			*out = append(*out, r.Person)
		}
	}
	return nil
}

func (p person) hasPhone(digits string) bool {
	for _, ph := range p.PhoneNumbers {
		v := ph.CanonicalForm
		if v == "" {
			v = ph.Value
		}
		d := onlyDigits(v)
		// Mamlakat kodisiz yozilgan raqamlar uchun oxirgi 9 raqam bo'yicha ham solishtiramiz.
		if d == digits || (len(d) >= 9 && len(digits) >= 9 && d[len(d)-9:] == digits[len(digits)-9:]) {
			return true
		}
	}
	return false
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) && r < 128 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---- Tasdiqlangan amallar ----

// EventPayload / TaskPayload — store.Action.Payload JSON'i.
type EventPayload struct {
	Title    string `json:"title"`
	Start    string `json:"start"` // RFC3339
	TZ       string `json:"tz"`
	Duration int    `json:"duration_min"`
	With     string `json:"with,omitempty"` // kontakt ismi (ko'rsatish uchun)
}

type TaskPayload struct {
	Title string `json:"title"`
	Due   string `json:"due,omitempty"` // YYYY-MM-DD
	With  string `json:"with,omitempty"`
}

// NewEventPayload / NewTaskPayload — model taklifidan saqlanadigan JSON.
func NewEventPayload(e llm.Event, with string) ([]byte, error) {
	return json.Marshal(EventPayload{
		Title: e.Title, Start: e.Start.Format(time.RFC3339), TZ: e.Start.Location().String(),
		Duration: int(e.Duration / time.Minute), With: with,
	})
}

func NewTaskPayload(t llm.Task, with string) ([]byte, error) {
	p := TaskPayload{Title: t.Title, With: with}
	if !t.Due.IsZero() {
		p.Due = t.Due.Format("2006-01-02")
	}
	return json.Marshal(p)
}

// Describe — egasiga ko'rsatiladigan qisqa matn (tasdiq so'rovi).
func Describe(a store.Action) string {
	switch a.Kind {
	case store.ActionEvent:
		var p EventPayload
		if json.Unmarshal(a.Payload, &p) != nil {
			return ""
		}
		start, err := time.Parse(time.RFC3339, p.Start)
		if err != nil {
			return ""
		}
		if loc, err := time.LoadLocation(p.TZ); err == nil {
			start = start.In(loc)
		}
		s := fmt.Sprintf("📅 %s\n%s, %s–%s", p.Title, start.Format("02.01.2006"), start.Format("15:04"),
			start.Add(time.Duration(p.Duration)*time.Minute).Format("15:04"))
		if p.With != "" {
			s += "\nKim bilan: " + p.With
		}
		return s + "\n\nTaqvimga qo'shaymi?"
	case store.ActionTask:
		var p TaskPayload
		if json.Unmarshal(a.Payload, &p) != nil {
			return ""
		}
		s := "✅ Vazifa: " + p.Title
		if p.Due != "" {
			if d, err := time.Parse("2006-01-02", p.Due); err == nil {
				s += "\nMuddat: " + d.Format("02.01.2006")
			}
		}
		if p.With != "" {
			s += "\nKimdan: " + p.With
		}
		return s + "\n\nGoogle Tasks'ga qo'shaymi?"
	}
	return ""
}

// Execute egasi tasdiqlagan amalni Google'da bajaradi; egasiga ko'rsatiladigan natija matnini qaytaradi.
func (c *Client) Execute(ctx context.Context, a store.Action) (string, error) {
	switch a.Kind {
	case store.ActionEvent:
		var p EventPayload
		if err := json.Unmarshal(a.Payload, &p); err != nil {
			return "", err
		}
		start, err := time.Parse(time.RFC3339, p.Start)
		if err != nil {
			return "", err
		}
		if loc, err := time.LoadLocation(p.TZ); err == nil {
			start = start.In(loc)
		}
		if _, err := c.CreateEvent(ctx, a.OwnerID, llm.Event{Title: p.Title, Start: start, Duration: time.Duration(p.Duration) * time.Minute}); err != nil {
			return "", err
		}
		return "Google Calendar'ga qo'shildi ✅", nil
	case store.ActionTask:
		var p TaskPayload
		if err := json.Unmarshal(a.Payload, &p); err != nil {
			return "", err
		}
		t := llm.Task{Title: p.Title}
		if p.Due != "" {
			t.Due, _ = time.Parse("2006-01-02", p.Due)
		}
		if err := c.CreateTask(ctx, a.OwnerID, t); err != nil {
			return "", err
		}
		return "Google Tasks'ga qo'shildi ✅", nil
	}
	return "", errors.New("noma'lum amal")
}

// UserMessage xatoni egasiga tushunarli matnga aylantiradi.
func UserMessage(err error) string {
	switch {
	case errors.Is(err, ErrNotConnected):
		return "Google ulanmagan yoki ruxsat bekor qilingan. Ilovada Google'ni qayta ulang."
	case errors.Is(err, ErrNoScope):
		return "Google bu amal uchun ruxsat bermagan. Ilovada Google'ni qayta ulab, barcha ruxsatlarni belgilang."
	default:
		return "Google'ga qo'shib bo'lmadi, keyinroq qayta urinib ko'ring."
	}
}
