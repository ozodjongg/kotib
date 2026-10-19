package miniapp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/plugin"
	"github.com/ozodjongg/secretar_ai_agent/internal/schedule"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// Scheduler — «Hozir bajarish» (schedule.Runner).
type Scheduler interface {
	RunNow(ctx context.Context, owner, id int64) error
}

// ---- Rejalashtirilgan vazifalar ----

type scheduleOut struct {
	ID         int64      `json:"id"`
	Title      string     `json:"title"`
	Prompt     string     `json:"prompt"`
	Kind       string     `json:"kind"`
	At         string     `json:"at"`
	Date       string     `json:"date,omitempty"`
	Weekdays   int        `json:"weekdays"`
	Enabled    bool       `json:"enabled"`
	Describe   string     `json:"describe"`
	NextRun    *time.Time `json:"next_run"`
	LastRun    *time.Time `json:"last_run"`
	LastStatus string     `json:"last_status"`
	LastResult string     `json:"last_result"`
	Runs       int        `json:"runs"`
	TargetID   int64      `json:"target_id"`
	TargetName string     `json:"target_name"`
	TargetAuto bool       `json:"target_auto"`
}

func toScheduleOut(s store.Schedule) scheduleOut {
	return scheduleOut{ID: s.ID, Title: s.Title, Prompt: s.Prompt, Kind: string(s.Kind), At: s.At, Date: s.Date,
		Weekdays: s.Weekdays, Enabled: s.Enabled, Describe: schedule.Describe(s), NextRun: s.NextRun, LastRun: s.LastRun,
		LastStatus: s.LastStatus, LastResult: s.LastResult, Runs: s.Runs,
		TargetID: s.TargetID, TargetName: s.TargetName, TargetAuto: s.TargetAuto}
}

type scheduleIn struct {
	Title    string `json:"title"`
	Prompt   string `json:"prompt"`
	Kind     string `json:"kind"`
	At       string `json:"at"`
	Date     string `json:"date"`
	Weekdays int    `json:"weekdays"`
	Enabled  *bool  `json:"enabled"`
	// TargetID — natijani yuborish uchun kontakt (0 — faqat menga); nil — o'zgarmaydi.
	TargetID   *int64 `json:"target_id"`
	TargetAuto *bool  `json:"target_auto"`
}

func (s *Server) schedulesOn(w http.ResponseWriter) bool {
	if s.Schedules == nil {
		httpErr(w, http.StatusNotFound, "rejalashtirilgan vazifalar o'chiq")
		return false
	}
	return true
}

// errNeedAdvanced — rejalashtirilgan vazifalar Advanced (va Pro) tarifda.
const errNeedAdvanced = "rejalashtirilgan vazifalar Advanced tarifda — obunani yoqing"

// entitled — obuna faolmi (Advanced yoki Pro, sinov ham). To'lov o'chiq bo'lsa — doim.
func (s *Server) entitled(ctx context.Context, owner int64) bool {
	if s.Billing == nil {
		return true
	}
	bs, err := s.Billing.Status(ctx, owner)
	return err == nil && bs.Active
}

// requireAdvanced — vazifa yaratish/o'zgartirish/bajarish uchun obuna kerak (402).
func (s *Server) requireAdvanced(w http.ResponseWriter, r *http.Request, owner int64) bool {
	if !s.entitled(r.Context(), owner) {
		httpErr(w, http.StatusPaymentRequired, errNeedAdvanced)
		return false
	}
	return true
}

func (s *Server) ownerLoc(ctx context.Context, owner int64) *time.Location {
	if st, err := s.Store.GetSettings(ctx, owner); err == nil {
		return st.Location()
	}
	return time.UTC
}

func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request, owner int64) {
	if !s.schedulesOn(w) {
		return
	}
	list, err := s.Store.ListSchedules(r.Context(), owner)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	out := make([]scheduleOut, 0, len(list))
	for _, sc := range list {
		out = append(out, toScheduleOut(sc))
	}
	var tools []string
	if s.Plugins != nil && s.Vault != nil && s.hasPro(r.Context(), owner) {
		for _, e := range s.Plugins.EnabledFor(r.Context(), s.Store, s.Vault, owner) {
			if p, ok := s.Plugins.Get(e.ID); ok {
				tools = append(tools, p.Info().Icon+" "+p.Info().Name)
			}
		}
	}
	writeJSON(w, 200, map[string]any{"items": out, "max": schedule.MaxPerOwner, "tz": s.ownerLoc(r.Context(), owner).String(),
		"plugins": tools, "allowed": s.entitled(r.Context(), owner), "plugins_allowed": s.hasPro(r.Context(), owner)})
}

// applySchedule kiritilgan maydonlarni tekshiradi va keyingi bajarilish vaqtini hisoblaydi.
func (s *Server) applySchedule(w http.ResponseWriter, r *http.Request, owner int64, sc *store.Schedule, in scheduleIn) bool {
	sc.Title, sc.Prompt, sc.Kind, sc.At, sc.Date, sc.Weekdays = in.Title, in.Prompt, store.ScheduleKind(in.Kind), in.At, in.Date, in.Weekdays
	if in.Enabled != nil {
		sc.Enabled = *in.Enabled
	}
	if !s.applyTarget(w, r, owner, sc, in) {
		return false
	}
	err := schedule.Normalize(sc)
	if err == nil {
		err = schedule.Plan(sc, time.Now(), s.ownerLoc(r.Context(), owner))
	}
	var inv *schedule.ErrInvalid
	if errors.As(err, &inv) {
		httpErr(w, 400, inv.Msg)
		return false
	} else if err != nil {
		httpErr(w, 500, "xatolik")
		return false
	}
	return true
}

// applyTarget — natija qabul qiluvchisi: faqat egasining Telegram kontakti (Pro). Kontakt nomi
// ro'yxatdan olinadi; o'zgarmagan qabul qiluvchi qayta tekshirilmaydi (sessiya uzilgan bo'lsa ham saqlash mumkin).
func (s *Server) applyTarget(w http.ResponseWriter, r *http.Request, owner int64, sc *store.Schedule, in scheduleIn) bool {
	if in.TargetAuto != nil {
		sc.TargetAuto = *in.TargetAuto
	}
	if in.TargetID == nil || *in.TargetID == sc.TargetID {
		if sc.TargetID == 0 {
			sc.TargetAuto = false
		}
		return true
	}
	id := *in.TargetID
	if id == 0 {
		sc.TargetID, sc.TargetName, sc.TargetAuto = 0, "", false
		return true
	}
	if !s.hasPro(r.Context(), owner) {
		httpErr(w, http.StatusPaymentRequired, "natijani kontaktga yuborish Pro tarifda")
		return false
	}
	if id == owner || id < 0 {
		httpErr(w, 400, "qabul qiluvchi noto'g'ri")
		return false
	}
	contacts, err := s.Ctl.Contacts(r.Context(), owner)
	if err != nil {
		httpErr(w, http.StatusConflict, "kontaktlar ro'yxati olinmadi — Telegram akkaunt ulanganini tekshiring")
		return false
	}
	for _, c := range contacts {
		if c.ID == id {
			sc.TargetID, sc.TargetName = id, c.Name
			return true
		}
	}
	httpErr(w, 400, "faqat Telegram kontaktlaringizdan birini tanlash mumkin")
	return false
}

func (s *Server) handleCreateSchedule(w http.ResponseWriter, r *http.Request, owner int64) {
	if !s.schedulesOn(w) || !s.requireConsent(w, r, owner) || !s.requireAdvanced(w, r, owner) {
		return
	}
	var in scheduleIn
	if !readJSON(w, r, &in) {
		return
	}
	list, err := s.Store.ListSchedules(r.Context(), owner)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	if len(list) >= schedule.MaxPerOwner {
		httpErr(w, 400, "ko'pi bilan "+strconv.Itoa(schedule.MaxPerOwner)+" ta vazifa — keraksizini o'chiring")
		return
	}
	sc := store.Schedule{OwnerID: owner, Enabled: true}
	if !s.applySchedule(w, r, owner, &sc, in) {
		return
	}
	id, err := s.Store.CreateSchedule(r.Context(), sc)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	s.Store.Audit(r.Context(), owner, "schedule_created", string(sc.Kind))
	sc, _ = s.Store.GetSchedule(r.Context(), owner, id)
	writeJSON(w, 200, toScheduleOut(sc))
}

func (s *Server) handleUpdateSchedule(w http.ResponseWriter, r *http.Request, owner int64) {
	if !s.schedulesOn(w) || !s.requireConsent(w, r, owner) || !s.requireAdvanced(w, r, owner) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		httpErr(w, 400, "id noto'g'ri")
		return
	}
	var in scheduleIn
	if !readJSON(w, r, &in) {
		return
	}
	sc, err := s.Store.GetSchedule(r.Context(), owner, id)
	if err != nil {
		httpErr(w, 404, "vazifa topilmadi")
		return
	}
	if !s.applySchedule(w, r, owner, &sc, in) {
		return
	}
	if err := s.Store.UpdateSchedule(r.Context(), sc); err != nil {
		httpErr(w, 404, "vazifa topilmadi")
		return
	}
	s.Store.Audit(r.Context(), owner, "schedule_updated", strconv.FormatInt(id, 10))
	sc, _ = s.Store.GetSchedule(r.Context(), owner, id)
	writeJSON(w, 200, toScheduleOut(sc))
}

func (s *Server) handleDeleteSchedule(w http.ResponseWriter, r *http.Request, owner int64) {
	if !s.schedulesOn(w) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		httpErr(w, 400, "id noto'g'ri")
		return
	}
	if err := s.Store.DeleteSchedule(r.Context(), owner, id); err != nil {
		httpErr(w, 404, "vazifa topilmadi")
		return
	}
	s.Store.Audit(r.Context(), owner, "schedule_deleted", strconv.FormatInt(id, 10))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleRunSchedule(w http.ResponseWriter, r *http.Request, owner int64) {
	if !s.schedulesOn(w) || !s.requireConsent(w, r, owner) || !s.requireAdvanced(w, r, owner) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		httpErr(w, 400, "id noto'g'ri")
		return
	}
	err := s.Schedules.RunNow(context.WithoutCancel(r.Context()), owner, id)
	switch {
	case err == nil:
		writeJSON(w, 200, map[string]bool{"ok": true})
	case errors.Is(err, store.ErrNotFound):
		httpErr(w, 404, "vazifa topilmadi")
	case errors.Is(err, schedule.ErrBusy):
		httpErr(w, 409, err.Error())
	case errors.Is(err, schedule.ErrNotEntitle):
		httpErr(w, http.StatusPaymentRequired, errNeedAdvanced)
	case errors.Is(err, schedule.ErrDailyLimit):
		httpErr(w, http.StatusTooManyRequests, err.Error())
	default:
		s.logger().Warn("run schedule", "owner", owner, "err", err)
		httpErr(w, 500, "xatolik")
	}
}

// ---- Plaginlar ----

type pluginOut struct {
	plugin.Info
	Enabled    bool              `json:"enabled"`
	Configured bool              `json:"configured"`
	Config     map[string]string `json:"config"`
	// Account — akkaunt orqali ulangan plaginda: qaysi akkaunt (email).
	Account string `json:"account,omitempty"`
}

// PluginAuth — plaginni foydalanuvchi akkaunti orqali ulash (google.Client): mijoz API kalit
// kiritmaydi, Google rozilik sahifasida «Ruxsat berish»ni bosadi.
type PluginAuth interface {
	PluginAuthURL(owner int64, plugin string) (string, error)
	// StatePurpose — OAuth state qaysi plaginga tegishli (bo'sh — asosiy Google ulanishi).
	StatePurpose(state string) string
	RevokeToken(ctx context.Context, token string)
}

func newPluginOut(info plugin.Info, enabled bool, cfg plugin.Config) pluginOut {
	return pluginOut{Info: info, Enabled: enabled, Configured: true, Config: plugin.Public(info, cfg), Account: cfg[plugin.KeyAccount]}
}

// handleConnectPlugin — akkaunt orqali ulanadigan plagin (YouTube) uchun Google rozilik havolasi.
func (s *Server) handleConnectPlugin(w http.ResponseWriter, r *http.Request, owner int64) {
	if !s.pluginsOn(w) || !s.requireConsent(w, r, owner) {
		return
	}
	p, ok := s.Plugins.Get(r.PathValue("id"))
	if !ok || p.Info().Connect == "" || p.Info().Soon || s.PluginAuth == nil {
		httpErr(w, 404, "bu plaginni akkaunt orqali ulab bo'lmaydi")
		return
	}
	if !s.hasPro(r.Context(), owner) {
		httpErr(w, http.StatusPaymentRequired, "plaginlar Pro tarifda")
		return
	}
	u, err := s.PluginAuth.PluginAuthURL(owner, p.Info().ID)
	if err != nil {
		s.logger().Warn("plugin auth url", "owner", owner, "err", err)
		httpErr(w, 500, "havola yaratilmadi")
		return
	}
	writeJSON(w, 200, map[string]string{"url": u})
}

// revokePluginToken — akkaunt orqali ulangan plagin tokenini Google'da bekor qiladi.
func (s *Server) revokePluginToken(ctx context.Context, owner int64, id string, cfgs map[string]plugin.Config) {
	if s.PluginAuth == nil {
		return
	}
	if rt := cfgs[id][plugin.KeyRefreshToken]; rt != "" {
		s.PluginAuth.RevokeToken(ctx, rt)
	}
}

// revokeAllPluginTokens — akkaunt o'chirilganda barcha plagin tokenlari bekor qilinadi.
func (s *Server) revokeAllPluginTokens(ctx context.Context, owner int64) {
	if s.Vault == nil || s.PluginAuth == nil {
		return
	}
	_, cfgs, err := plugin.Load(ctx, s.Store, s.Vault, owner)
	if err != nil {
		return
	}
	for id := range cfgs {
		s.revokePluginToken(ctx, owner, id, cfgs)
	}
}

func (s *Server) pluginsOn(w http.ResponseWriter) bool {
	if s.Plugins == nil || s.Vault == nil {
		httpErr(w, http.StatusNotFound, "plaginlar o'chiq")
		return false
	}
	return true
}

func (s *Server) handleListPlugins(w http.ResponseWriter, r *http.Request, owner int64) {
	if !s.pluginsOn(w) {
		return
	}
	recs, cfgs, err := plugin.Load(r.Context(), s.Store, s.Vault, owner)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	cat := s.Plugins.Catalog()
	out := make([]pluginOut, 0, len(cat))
	for _, info := range cat {
		rec, ok := recs[info.ID]
		o := pluginOut{Info: info, Config: map[string]string{}}
		if ok && !info.Soon {
			o = newPluginOut(info, rec.Enabled, cfgs[info.ID])
		}
		out = append(out, o)
	}
	writeJSON(w, 200, map[string]any{"items": out, "allowed": s.hasPro(r.Context(), owner)})
}

func (s *Server) handleSavePlugin(w http.ResponseWriter, r *http.Request, owner int64) {
	if !s.pluginsOn(w) || !s.requireConsent(w, r, owner) {
		return
	}
	p, ok := s.Plugins.Get(r.PathValue("id"))
	if !ok {
		httpErr(w, 404, "plagin topilmadi")
		return
	}
	var in struct {
		Enabled bool              `json:"enabled"`
		Config  map[string]string `json:"config"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	// Plaginlar — Pro tarifda. O'chirib qo'yish va uzish har doim mumkin.
	if in.Enabled && !s.hasPro(r.Context(), owner) {
		httpErr(w, http.StatusPaymentRequired, "plaginlar Pro tarifda")
		return
	}
	if len(in.Config) > 20 {
		httpErr(w, 400, "sozlama noto'g'ri")
		return
	}
	info := p.Info()
	_, old, err := plugin.Load(r.Context(), s.Store, s.Vault, owner)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	cfg, err := plugin.Clean(info, in.Config, old[info.ID])
	var inv *plugin.ErrInvalid
	if errors.As(err, &inv) {
		httpErr(w, 400, inv.Msg)
		return
	} else if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	if v, ok := p.(plugin.Validator); ok && in.Enabled {
		if err := v.Validate(cfg); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
	}
	sealed, err := plugin.Seal(s.Vault, owner, info.ID, cfg)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	if err := s.Store.SavePluginConfig(r.Context(), store.PluginConfig{OwnerID: owner, PluginID: info.ID, Enabled: in.Enabled, Config: sealed}); err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	ev := "plugin_disabled"
	if in.Enabled {
		ev = "plugin_enabled"
	}
	s.Store.Audit(r.Context(), owner, ev, info.ID)
	writeJSON(w, 200, newPluginOut(info, in.Enabled, cfg))
}

func (s *Server) handleDeletePlugin(w http.ResponseWriter, r *http.Request, owner int64) {
	if !s.pluginsOn(w) {
		return
	}
	p, ok := s.Plugins.Get(r.PathValue("id"))
	if !ok {
		httpErr(w, 404, "plagin topilmadi")
		return
	}
	if _, cfgs, err := plugin.Load(r.Context(), s.Store, s.Vault, owner); err == nil {
		s.revokePluginToken(context.WithoutCancel(r.Context()), owner, p.Info().ID, cfgs)
	}
	if err := s.Store.DeletePluginConfig(r.Context(), owner, p.Info().ID); err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	s.Store.Audit(r.Context(), owner, "plugin_removed", p.Info().ID)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
