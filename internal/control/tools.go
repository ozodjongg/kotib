package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
)

// call — bitta vosita chaqiruvi konteksti: kim (user), qaysi akkaunt va argumentlar.
type call struct {
	a       *Agent
	user    int64
	account int64
	args    map[string]any
}

// tool — modelga beriladigan vosita va uni bajaruvchi.
type tool struct {
	decl llm.ToolDecl
	run  func(ctx context.Context, c *call) (string, error)
}

// ---- argumentlar ----

func (c *call) str(k string) string {
	switch v := c.args[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func (c *call) num(k string) (int64, bool) {
	switch v := c.args[k].(type) {
	case float64:
		return int64(v), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n, err == nil
	}
	return 0, false
}

func (c *call) id(k string) (int64, error) {
	n, ok := c.num(k)
	if !ok || n <= 0 {
		return 0, fmt.Errorf("%s ko'rsatilmagan", k)
	}
	return n, nil
}

func (c *call) boolean(k string) (bool, bool) {
	v, ok := c.args[k].(bool)
	return v, ok
}

// jsonObj — "changes_json" kabi JSON matn argumenti.
func (c *call) jsonObj(k string) (map[string]any, error) {
	raw := c.str(k)
	if raw == "" {
		return nil, fmt.Errorf("%s bo'sh", k)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("%s yaroqli JSON obyekt emas: %v", k, err)
	}
	return m, nil
}

// ---- API ----

// api — Mini App API'sini chaqiradi; xato holatida server matnini qaytaradi.
func (c *call) api(ctx context.Context, method, path string, body any) ([]byte, error) {
	code, out := c.a.API.Invoke(ctx, c.user, c.account, method, path, body)
	if code >= 200 && code < 300 {
		return out, nil
	}
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(out, &e) == nil && e.Error != "" {
		if code == http.StatusPaymentRequired {
			return nil, errors.New(e.Error + " (tarif: /obuna)")
		}
		return nil, errors.New(e.Error)
	}
	return nil, fmt.Errorf("server javobi %d", code)
}

func (c *call) text(ctx context.Context, method, path string, body any) (string, error) {
	out, err := c.api(ctx, method, path, body)
	return string(out), err
}

// ---- yordamchilar ----

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "OBJECT", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func strP(desc string) map[string]any { return map[string]any{"type": "STRING", "description": desc} }
func intP(desc string) map[string]any { return map[string]any{"type": "INTEGER", "description": desc} }
func boolP(desc string) map[string]any {
	return map[string]any{"type": "BOOLEAN", "description": desc}
}
func enumP(desc string, vals ...string) map[string]any {
	return map[string]any{"type": "STRING", "description": desc, "enum": vals}
}

// merge — patch'ni base ustiga yozadi: obyektlar ichma-ich birlashtiriladi, qolganlari almashtiriladi.
func merge(base, patch map[string]any) {
	for k, v := range patch {
		if pm, ok := v.(map[string]any); ok {
			if bm, ok := base[k].(map[string]any); ok {
				merge(bm, pm)
				continue
			}
		}
		base[k] = v
	}
}

// serverOnly — sozlamalarning faqat server yozadigan maydonlari (model o'zgartira olmaydi).
var serverOnly = []string{"style_updated_at", "style_score", "shadow_until", "away", "away_until"}

// isoDays — 1=dushanba … 7=yakshanba ro'yxatini jadval bitmaskiga (bit 0 — yakshanba) o'giradi.
func isoDays(v any) (int, error) {
	list, ok := v.([]any)
	if !ok {
		return 0, errors.New("weekdays — 1..7 raqamlar ro'yxati (1=dushanba)")
	}
	mask := 0
	for _, x := range list {
		f, ok := x.(float64)
		if !ok || f < 1 || f > 7 {
			return 0, errors.New("weekdays — 1..7 raqamlar ro'yxati (1=dushanba)")
		}
		mask |= 1 << (int(f) % 7)
	}
	return mask, nil
}

// ---- katalog ----

func catalog() []tool {
	scheduleProps := map[string]any{
		"title":       strP("qisqa nom (ixtiyoriy)"),
		"prompt":      strP("AI bajaradigan ko'rsatma, masalan: «Toshkent ob-havosi va dollar kursini yoz»"),
		"kind":        enumP("takrorlanish: once — bir marta, hourly — har soat, daily — har kuni, weekdays — dushanba–juma, weekly — tanlangan kunlar", "once", "hourly", "daily", "weekdays", "weekly"),
		"at":          strP("vaqt HH:MM (egasining vaqti bilan)"),
		"date":        strP("sana YYYY-MM-DD (faqat once uchun)"),
		"weekdays":    map[string]any{"type": "ARRAY", "items": map[string]any{"type": "INTEGER"}, "description": "weekly uchun kunlar: 1=dushanba … 7=yakshanba"},
		"target_id":   intP("natija egasi nomidan yuboriladigan kontakt ID'si (find_contacts); 0 — faqat egasiga"),
		"target_auto": boolP("true — kontaktga darhol yuboriladi, false — avval qoralama"),
	}
	return []tool{
		{llm.ToolDecl{Name: "get_overview", Description: "Umumiy holat: kotib holati (faol/to'xtatilgan), kutilayotgan qoralamalar soni, obuna, Google, bu oy statistikasi, akkauntlar, uslub (persona) presetlari."},
			func(ctx context.Context, c *call) (string, error) { return c.text(ctx, "GET", "/api/me", nil) }},
		{llm.ToolDecl{Name: "get_settings", Description: "Joriy sozlamalarni JSON ko'rinishida o'qish (rejim, tinch soatlar, qoidalar, persona, bilim bazasi, o'zim haqimda, kun tartibi, kontakt eslatmalari va teglari, bildirishnomalar)."},
			func(ctx context.Context, c *call) (string, error) { return c.text(ctx, "GET", "/api/settings", nil) }},
		{llm.ToolDecl{Name: "update_settings", Description: "Sozlamalarni o'zgartirish: faqat o'zgaradigan maydonlar beriladi, qolgani saqlanadi " +
			"(obyektlar ichma-ich birlashtiriladi, ro'yxatlar — masalan rules, routine — to'liq almashtiriladi, shuning uchun avval get_settings bilan o'qing). " +
			"Asosiy maydonlar: mode (off|draft|auto), reply_unknown, reply_groups, reply_bots, allow_links, drafts_in_bot, no_suggest, reply_voice, reply_photos, " +
			"transcribe_voice, no_digest, smart_digest, morning_brief, no_weekly, no_followups, no_urgent, no_unanswered, unanswered_hours (0–48), no_birthdays, learn, " +
			"style_profile, knowledge (bilim bazasi), about (o'zim haqimda), routine ([{days,start,end,activity}]), default_persona (preset kaliti, custom yoki mine), " +
			"default_custom, owner_name, language (uz|ru|en|auto), quiet_hours {enabled,start,end,tz}, rules ([{id,title,match{user_ids,tags,is_contact},mode,persona,custom,disabled}]), " +
			"contact_notes {\"<kontakt id>\": \"eslatma\"} (bo'sh matn — o'chiradi), contact_tags {\"<kontakt id>\": [\"teg\"]}. Band holat uchun set_away ishlating.",
			Params: obj(map[string]any{"changes_json": strP("o'zgarishlar JSON obyekt matni, masalan {\"mode\":\"auto\",\"quiet_hours\":{\"start\":\"22:00\"}}")}, "changes_json")},
			updateSettings},
		{llm.ToolDecl{Name: "set_away", Description: "Hozirgi holat (kotib javobda hisobga oladi): masalan «rulda», «yig'ilishda». Bo'sh matn — holatni olib tashlash.",
			Params: obj(map[string]any{"text": strP("holat, bo'sh — tozalash"), "minutes": intP("necha daqiqadan keyin o'zi tugaydi (0 — muddatsiz, ko'pi 1440)")}, "text")},
			func(ctx context.Context, c *call) (string, error) {
				m, _ := c.num("minutes")
				return c.text(ctx, "POST", "/api/away", map[string]any{"away": c.str("text"), "minutes": m})
			}},
		{llm.ToolDecl{Name: "pause", Description: "Kotibni to'xtatish (hech kimga javob yozmaydi)."},
			func(ctx context.Context, c *call) (string, error) { return c.text(ctx, "POST", "/api/pause", nil) }},
		{llm.ToolDecl{Name: "resume", Description: "To'xtatilgan kotibni davom ettirish."},
			func(ctx context.Context, c *call) (string, error) { return c.text(ctx, "POST", "/api/resume", nil) }},
		{llm.ToolDecl{Name: "list_drafts", Description: "Tasdiq kutayotgan javob qoralamalari (kontakt xabari va tayyorlangan javob)."},
			func(ctx context.Context, c *call) (string, error) { return c.text(ctx, "GET", "/api/drafts", nil) }},
		{llm.ToolDecl{Name: "send_draft", Description: "Qoralamani kontaktga yuborish (ixtiyoriy: o'zgartirilgan matn bilan). Faqat egasi aniq so'raganda.",
			Params: obj(map[string]any{"id": intP("qoralama ID (list_drafts)"), "text": strP("yangi matn; bo'sh — qoralamadagi matn")}, "id")},
			func(ctx context.Context, c *call) (string, error) {
				id, err := c.id("id")
				if err != nil {
					return "", err
				}
				return c.text(ctx, "POST", fmt.Sprintf("/api/drafts/%d/send", id), map[string]any{"reply": c.str("text")})
			}},
		{llm.ToolDecl{Name: "reject_draft", Description: "Qoralamani rad etish (yuborilmaydi).",
			Params: obj(map[string]any{"id": intP("qoralama ID")}, "id")},
			func(ctx context.Context, c *call) (string, error) {
				id, err := c.id("id")
				if err != nil {
					return "", err
				}
				return c.text(ctx, "POST", fmt.Sprintf("/api/drafts/%d/reject", id), nil)
			}},
		{llm.ToolDecl{Name: "find_contacts", Description: "Telegram kontaktlaridan ism bo'yicha qidirish (ID kerak bo'lganda: qoidalar, eslatmalar, vazifa qabul qiluvchisi).",
			Params: obj(map[string]any{"query": strP("ism yoki uning qismi; bo'sh — birinchi 30 ta")})},
			findContacts},
		{llm.ToolDecl{Name: "recent_activity", Description: "Kotibning oxirgi harakatlari jurnali (yuborildi, qoralama, o'tkazib yuborildi...)."},
			func(ctx context.Context, c *call) (string, error) { return c.text(ctx, "GET", "/api/activity", nil) }},
		{llm.ToolDecl{Name: "get_stats", Description: "Statistika: javoblar soni, tejalgan vaqt, faol kontaktlar."},
			func(ctx context.Context, c *call) (string, error) { return c.text(ctx, "GET", "/api/stats", nil) }},
		{llm.ToolDecl{Name: "ask_chat", Description: "Kontakt bilan yozishma haqida savol (yozishmani o'qib javob beradi; Pro). Savolda kontakt ismi bo'lishi kerak, masalan «Aziz bilan nima kelishdik?».",
			Params: obj(map[string]any{"question": strP("savol, kontakt ismi bilan")}, "question")},
			func(ctx context.Context, c *call) (string, error) {
				if c.a.Ask == nil {
					return "", errors.New("bu funksiya o'chiq")
				}
				return c.a.Ask(ctx, c.user, c.str("question")), nil
			}},
		{llm.ToolDecl{Name: "list_schedules", Description: "Rejalashtirilgan AI vazifalar ro'yxati (jadval, oxirgi natija)."},
			func(ctx context.Context, c *call) (string, error) { return c.text(ctx, "GET", "/api/schedules", nil) }},
		{llm.ToolDecl{Name: "create_schedule", Description: "Yangi rejalashtirilgan AI vazifa (masalan, har kuni 8:00 da ob-havo va yangiliklar).",
			Params: obj(scheduleProps, "prompt", "kind", "at")},
			func(ctx context.Context, c *call) (string, error) {
				body, err := scheduleBody(c, map[string]any{})
				if err != nil {
					return "", err
				}
				return c.text(ctx, "POST", "/api/schedules", body)
			}},
		{llm.ToolDecl{Name: "update_schedule", Description: "Vazifani o'zgartirish: faqat o'zgaradigan maydonlar; enabled=false — vaqtincha o'chirish.",
			Params: obj(withProps(scheduleProps, map[string]any{"id": intP("vazifa ID"), "enabled": boolP("yoqilgan/o'chirilgan")}), "id")},
			updateSchedule},
		{llm.ToolDecl{Name: "delete_schedule", Description: "Vazifani o'chirish.", Params: obj(map[string]any{"id": intP("vazifa ID")}, "id")},
			func(ctx context.Context, c *call) (string, error) {
				id, err := c.id("id")
				if err != nil {
					return "", err
				}
				return c.text(ctx, "DELETE", fmt.Sprintf("/api/schedules/%d", id), nil)
			}},
		{llm.ToolDecl{Name: "run_schedule", Description: "Vazifani hozir bajarish (natija alohida xabar bo'lib keladi).", Params: obj(map[string]any{"id": intP("vazifa ID")}, "id")},
			func(ctx context.Context, c *call) (string, error) {
				id, err := c.id("id")
				if err != nil {
					return "", err
				}
				return c.text(ctx, "POST", fmt.Sprintf("/api/schedules/%d/run", id), nil)
			}},
		{llm.ToolDecl{Name: "list_plugins", Description: "Plaginlar (ob-havo, valyuta, YouTube, Sheets, Notion...): yoqilganmi, sozlamalari, maydonlari."},
			listPlugins},
		{llm.ToolDecl{Name: "save_plugin", Description: "Plaginni yoqish/o'chirish yoki oddiy sozlamasini o'zgartirish (masalan shahar). API kalitlar (secret maydonlar) faqat ilovada kiritiladi.",
			Params: obj(map[string]any{"id": strP("plagin ID (list_plugins)"), "enabled": boolP("yoqilsinmi"),
				"config_json": strP("o'zgaradigan maydonlar JSON obyekt matni, masalan {\"city\":\"Samarqand\"}; bo'sh — o'zgarmaydi")}, "id", "enabled")},
			savePlugin},
		{llm.ToolDecl{Name: "remove_plugin", Description: "Plaginni butunlay uzish (sozlamalari o'chadi).", Params: obj(map[string]any{"id": strP("plagin ID")}, "id")},
			func(ctx context.Context, c *call) (string, error) {
				return c.text(ctx, "DELETE", "/api/plugins/"+pathSeg(c.str("id")), nil)
			}},
		{llm.ToolDecl{Name: "connect_plugin", Description: "Akkaunt orqali ulanadigan plagin (YouTube, Sheets, Drive) uchun ulash havolasi.", Params: obj(map[string]any{"id": strP("plagin ID")}, "id")},
			func(ctx context.Context, c *call) (string, error) {
				return c.text(ctx, "POST", "/api/plugins/"+pathSeg(c.str("id"))+"/connect", nil)
			}},
		{llm.ToolDecl{Name: "google_connect", Description: "Google (taqvim, kontaktlar, vazifalar) ulash havolasi (Pro)."},
			func(ctx context.Context, c *call) (string, error) {
				return c.text(ctx, "POST", "/api/google/connect", nil)
			}},
		{llm.ToolDecl{Name: "google_disconnect", Description: "Google akkauntini uzish."},
			func(ctx context.Context, c *call) (string, error) {
				return c.text(ctx, "POST", "/api/google/disconnect", nil)
			}},
		{llm.ToolDecl{Name: "style_learn", Description: "Oxirgi yozishmalardan egasining yozish uslubini o'rganish («O'zimdek» persona; Pro). Natija alohida xabar bo'lib keladi."},
			func(ctx context.Context, c *call) (string, error) {
				return c.text(ctx, "POST", "/api/style/learn", nil)
			}},
		{llm.ToolDecl{Name: "style_shadow", Description: "Soya rejimi: kotib N kun jim javob tayyorlaydi va egasining javoblari bilan solishtirib o'rganadi (0 — o'chirish; Pro).",
			Params: obj(map[string]any{"days": intP("kunlar 0–14")}, "days")},
			func(ctx context.Context, c *call) (string, error) {
				d, _ := c.num("days")
				return c.text(ctx, "POST", "/api/style/shadow", map[string]any{"days": d})
			}},
		{llm.ToolDecl{Name: "billing_status", Description: "Obuna holati: tarif, sinov, to'lov sanasi."},
			func(ctx context.Context, c *call) (string, error) { return c.text(ctx, "GET", "/api/billing", nil) }},
		{llm.ToolDecl{Name: "subscribe", Description: "Obuna to'lov havolasi (Telegram Stars).", Params: obj(map[string]any{"tier": enumP("tarif", "advanced", "pro")}, "tier")},
			func(ctx context.Context, c *call) (string, error) {
				return c.text(ctx, "POST", "/api/billing/invoice", map[string]any{"tier": c.str("tier")})
			}},
		{llm.ToolDecl{Name: "cancel_subscription", Description: "Obunani bekor qilish (to'langan muddat oxirigacha ishlaydi). Faqat egasi aniq so'raganda."},
			func(ctx context.Context, c *call) (string, error) {
				return c.text(ctx, "POST", "/api/billing/cancel", nil)
			}},
		{llm.ToolDecl{Name: "renew_subscription", Description: "Bekor qilingan obunani qayta yoqish."},
			func(ctx context.Context, c *call) (string, error) {
				return c.text(ctx, "POST", "/api/billing/renew", nil)
			}},
		{llm.ToolDecl{Name: "referral", Description: "Do'stlarni taklif qilish havolasi va bonus natijalari."},
			func(ctx context.Context, c *call) (string, error) { return c.text(ctx, "GET", "/api/referral", nil) }},
		{llm.ToolDecl{Name: "charity", Description: "Hayriya hisoboti va egasining hissasi."},
			func(ctx context.Context, c *call) (string, error) { return c.text(ctx, "GET", "/api/charity", nil) }},
		{llm.ToolDecl{Name: "charity_donate", Description: "Hayriyaga xayriya qilish havolasi (Telegram Stars).", Params: obj(map[string]any{"stars": intP("yulduzlar soni")}, "stars")},
			func(ctx context.Context, c *call) (string, error) {
				n, _ := c.num("stars")
				return c.text(ctx, "POST", "/api/charity/donate", map[string]any{"stars": n})
			}},
		{llm.ToolDecl{Name: "list_accounts", Description: "Asosiy va qo'shimcha Telegram akkauntlari (ID va nomi)."},
			func(ctx context.Context, c *call) (string, error) { return c.text(ctx, "GET", "/api/accounts", nil) }},
		{llm.ToolDecl{Name: "use_account", Description: "Keyingi amallar qaysi akkauntga tegishli: qo'shimcha akkaunt ID'si yoki 0 — asosiy akkaunt.",
			Params: obj(map[string]any{"id": intP("akkaunt ID (list_accounts), 0 — asosiy")}, "id")},
			useAccount},
		{llm.ToolDecl{Name: "link_account", Description: "Qo'shimcha Telegram akkaunt ulash havolasi (Pro).", Params: obj(map[string]any{"label": strP("nomi, masalan «Ish»")})},
			func(ctx context.Context, c *call) (string, error) {
				return c.text(ctx, "POST", "/api/accounts/link", map[string]any{"label": c.str("label")})
			}},
		{llm.ToolDecl{Name: "unlink_account", Description: "Qo'shimcha akkauntni uzish. Faqat egasi aniq so'raganda.", Params: obj(map[string]any{"id": intP("akkaunt ID")}, "id")},
			func(ctx context.Context, c *call) (string, error) {
				id, err := c.id("id")
				if err != nil {
					return "", err
				}
				return c.text(ctx, "DELETE", fmt.Sprintf("/api/accounts/%d", id), nil)
			}},
	}
}

func withProps(base, extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// pathSeg — plagin ID'si yo'lga qo'yiladi: faqat oddiy belgilar.
func pathSeg(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return -1
	}, s)
}

func updateSettings(ctx context.Context, c *call) (string, error) {
	patch, err := c.jsonObj("changes_json")
	if err != nil {
		return "", err
	}
	for _, k := range serverOnly {
		delete(patch, k)
	}
	if len(patch) == 0 {
		return "", errors.New("o'zgarish yo'q (band holat uchun set_away)")
	}
	raw, err := c.api(ctx, "GET", "/api/settings", nil)
	if err != nil {
		return "", err
	}
	var cur map[string]any
	if err := json.Unmarshal(raw, &cur); err != nil {
		return "", err
	}
	merge(cur, patch)
	if _, err := c.api(ctx, "PUT", "/api/settings", cur); err != nil {
		return "", err
	}
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	return "saqlandi: " + strings.Join(keys, ", "), nil
}

func findContacts(ctx context.Context, c *call) (string, error) {
	raw, err := c.api(ctx, "GET", "/api/contacts", nil)
	if err != nil {
		return "", err
	}
	var cs []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &cs); err != nil {
		return "", err
	}
	q := strings.ToLower(c.str("query"))
	type row struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	out := []row{}
	for _, x := range cs {
		if q == "" || strings.Contains(strings.ToLower(x.Name), q) {
			out = append(out, row(x))
			if len(out) >= 30 {
				break
			}
		}
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

func scheduleBody(c *call, body map[string]any) (map[string]any, error) {
	for _, k := range []string{"title", "prompt", "kind", "at", "date"} {
		if _, ok := c.args[k]; ok {
			body[k] = c.str(k)
		}
	}
	if v, ok := c.args["weekdays"]; ok {
		mask, err := isoDays(v)
		if err != nil {
			return nil, err
		}
		body["weekdays"] = mask
	}
	if n, ok := c.num("target_id"); ok {
		body["target_id"] = n
	}
	if b, ok := c.boolean("target_auto"); ok {
		body["target_auto"] = b
	}
	if b, ok := c.boolean("enabled"); ok {
		body["enabled"] = b
	}
	return body, nil
}

func updateSchedule(ctx context.Context, c *call) (string, error) {
	id, err := c.id("id")
	if err != nil {
		return "", err
	}
	raw, err := c.api(ctx, "GET", "/api/schedules", nil)
	if err != nil {
		return "", err
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return "", err
	}
	var cur map[string]any
	for _, it := range list.Items {
		if v, _ := it["id"].(float64); int64(v) == id {
			cur = it
		}
	}
	if cur == nil {
		return "", errors.New("vazifa topilmadi")
	}
	// PUT barcha maydonlarni almashtiradi: joriy qiymatlar ustiga o'zgarishlar.
	body := map[string]any{}
	for _, k := range []string{"title", "prompt", "kind", "at", "date", "weekdays", "enabled", "target_id", "target_auto"} {
		if v, ok := cur[k]; ok {
			body[k] = v
		}
	}
	if body, err = scheduleBody(c, body); err != nil {
		return "", err
	}
	return c.text(ctx, "PUT", fmt.Sprintf("/api/schedules/%d", id), body)
}

type pluginField struct {
	Key  string `json:"key"`
	Type string `json:"type"`
}

type pluginItem struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Soon       bool              `json:"soon"`
	Connect    string            `json:"connect"`
	Enabled    bool              `json:"enabled"`
	Configured bool              `json:"configured"`
	Account    string            `json:"account"`
	Config     map[string]string `json:"config"`
	Fields     []pluginField     `json:"fields"`
}

func (c *call) plugins(ctx context.Context) ([]pluginItem, error) {
	raw, err := c.api(ctx, "GET", "/api/plugins", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []pluginItem `json:"items"`
	}
	err = json.Unmarshal(raw, &out)
	return out.Items, err
}

func listPlugins(ctx context.Context, c *call) (string, error) {
	items, err := c.plugins(ctx)
	if err != nil {
		return "", err
	}
	type row struct {
		ID         string            `json:"id"`
		Name       string            `json:"name"`
		Enabled    bool              `json:"enabled"`
		Configured bool              `json:"configured"`
		ViaAccount bool              `json:"via_account,omitempty"`
		Account    string            `json:"account,omitempty"`
		Config     map[string]string `json:"config,omitempty"`
		Fields     []pluginField     `json:"fields,omitempty"`
	}
	out := []row{}
	for _, p := range items {
		if p.Soon {
			continue
		}
		cfg := map[string]string{}
		for _, f := range p.Fields {
			if v, ok := p.Config[f.Key]; ok && f.Type != "secret" {
				cfg[f.Key] = v
			} else if ok {
				cfg[f.Key] = "(kiritilgan)"
			}
		}
		out = append(out, row{p.ID, p.Name, p.Enabled, p.Configured, p.Connect != "", p.Account, cfg, p.Fields})
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

func savePlugin(ctx context.Context, c *call) (string, error) {
	id := pathSeg(c.str("id"))
	items, err := c.plugins(ctx)
	if err != nil {
		return "", err
	}
	var p *pluginItem
	for i := range items {
		if items[i].ID == id {
			p = &items[i]
		}
	}
	if p == nil {
		return "", errors.New("plagin topilmadi")
	}
	enabled, _ := c.boolean("enabled")
	cfg := map[string]string{}
	secret := map[string]bool{}
	for _, f := range p.Fields {
		if f.Type == "secret" {
			secret[f.Key] = true
			continue // bo'sh qoldirilsa server eskisini saqlaydi
		}
		if v, ok := p.Config[f.Key]; ok {
			cfg[f.Key] = v
		}
	}
	if c.str("config_json") != "" {
		patch, err := c.jsonObj("config_json")
		if err != nil {
			return "", err
		}
		for k, v := range patch {
			if secret[k] {
				return "", errors.New("API kalitlar faqat ilovada kiritiladi (Plaginlar bo'limi)")
			}
			cfg[k] = fmt.Sprint(v)
		}
	}
	return c.text(ctx, "PUT", "/api/plugins/"+id, map[string]any{"enabled": enabled, "config": cfg})
}

func useAccount(ctx context.Context, c *call) (string, error) {
	id, _ := c.num("id")
	if id != 0 && id != c.user {
		raw, err := c.api(ctx, "GET", "/api/accounts", nil)
		if err != nil {
			return "", err
		}
		var out struct {
			Accounts []struct {
				ID int64 `json:"id"`
			} `json:"accounts"`
		}
		_ = json.Unmarshal(raw, &out)
		found := false
		for _, a := range out.Accounts {
			found = found || a.ID == id
		}
		if !found {
			return "", errors.New("bunday akkaunt yo'q")
		}
	} else {
		id = 0
	}
	c.account = id
	c.a.setAccount(c.user, id)
	if id == 0 {
		return "endi asosiy akkaunt", nil
	}
	return "endi qo'shimcha akkaunt " + strconv.FormatInt(id, 10), nil
}
