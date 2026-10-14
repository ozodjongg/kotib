package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
)

// Memory — dev/test uchun. Qayta ishga tushganda hamma narsa yo'qoladi.
type Memory struct {
	mu       sync.Mutex
	accounts map[int64]Account
	sessions map[int64][]byte
	settings map[int64]config.Settings
	drafts   map[int64]Draft
	nextID   int64
	audit    []auditRow
	subs     map[int64]Subscription
	payments map[string]Payment
	usage    map[usageKey]int
	google   map[int64]GoogleLink
	actions  map[int64]Action
	hooks    map[int64]string // owner → token xeshi
}

type usageKey struct {
	owner int64
	day   string
}

type auditRow struct {
	Owner         int64
	At            time.Time
	Event, Detail string
}

func NewMemory() *Memory {
	return &Memory{
		accounts: map[int64]Account{},
		sessions: map[int64][]byte{},
		settings: map[int64]config.Settings{},
		drafts:   map[int64]Draft{},
		subs:     map[int64]Subscription{},
		payments: map[string]Payment{},
		usage:    map[usageKey]int{},
		google:   map[int64]GoogleLink{},
		actions:  map[int64]Action{},
		hooks:    map[int64]string{},
	}
}

func (m *Memory) UpsertAccount(_ context.Context, a Account) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.accounts[a.OwnerID]; ok {
		if a.CreatedAt.IsZero() {
			a.CreatedAt = old.CreatedAt
		}
		if a.ConsentAt == nil {
			a.ConsentAt = old.ConsentAt
		}
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	m.accounts[a.OwnerID] = a
	return nil
}

func (m *Memory) GetAccount(_ context.Context, owner int64) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[owner]
	if !ok {
		return Account{}, ErrNotFound
	}
	return a, nil
}

func (m *Memory) SetStatus(_ context.Context, owner int64, st AccountStatus, note string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[owner]
	if !ok {
		return ErrNotFound
	}
	a.Status, a.StatusNote = st, note
	m.accounts[owner] = a
	return nil
}

func (m *Memory) ListActiveOwners(_ context.Context) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []int64
	for id, a := range m.accounts {
		if a.Status == StatusActive && a.ConsentAt != nil {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func (m *Memory) DeleteAccount(_ context.Context, owner int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.accounts, owner)
	delete(m.sessions, owner)
	delete(m.settings, owner)
	delete(m.google, owner)
	delete(m.hooks, owner)
	for id, a := range m.actions {
		if a.OwnerID == owner {
			delete(m.actions, id)
		}
	}
	for id, d := range m.drafts {
		if d.OwnerID == owner {
			delete(m.drafts, id)
		}
	}
	kept := m.audit[:0]
	for _, r := range m.audit {
		if r.Owner != owner {
			kept = append(kept, r)
		}
	}
	m.audit = kept
	return nil
}

func (m *Memory) SaveSession(_ context.Context, owner int64, blob []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[owner] = append([]byte(nil), blob...)
	return nil
}

func (m *Memory) LoadSession(_ context.Context, owner int64) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.sessions[owner]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), b...), nil
}

func (m *Memory) GetSettings(_ context.Context, owner int64) (config.Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.settings[owner]
	if !ok {
		return config.Default(), nil // yangi mijoz: xavfsiz default
	}
	return s, nil
}

func (m *Memory) SaveSettings(_ context.Context, owner int64, s config.Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[owner] = s
	return nil
}

func (m *Memory) CreateDraft(_ context.Context, d Draft) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	d.ID = m.nextID
	d.Status = DraftPending
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	m.drafts[d.ID] = d
	return d.ID, nil
}

func (m *Memory) GetDraft(_ context.Context, owner, id int64) (Draft, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.drafts[id]
	if !ok || d.OwnerID != owner { // boshqa mijozning draftini ko'rib bo'lmaydi
		return Draft{}, ErrNotFound
	}
	return d, nil
}

func (m *Memory) ListDrafts(_ context.Context, owner int64, st DraftStatus, limit int) ([]Draft, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Draft
	for _, d := range m.drafts {
		if d.OwnerID == owner && d.Status == st {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) ResolveDraft(_ context.Context, owner, id int64, st DraftStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.drafts[id]
	if !ok || d.OwnerID != owner {
		return ErrNotFound
	}
	if d.Status != DraftPending {
		return ErrNotFound // allaqachon hal qilingan: ikki marta yuborilmasin
	}
	d.Status = st
	m.drafts[id] = d
	return nil
}

func (m *Memory) Audit(_ context.Context, owner int64, event, detail string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audit = append(m.audit, auditRow{owner, time.Now(), event, detail})
}

func (m *Memory) ReopenDraft(_ context.Context, owner, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.drafts[id]
	if !ok || d.OwnerID != owner || d.Status != DraftSent {
		return ErrNotFound
	}
	d.Status = DraftPending
	m.drafts[id] = d
	return nil
}

func (m *Memory) ExpireDrafts(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for id, d := range m.drafts {
		if !d.CreatedAt.Before(before) {
			continue
		}
		if d.Status == DraftPending {
			d.Status, d.Incoming, d.Reply = DraftExpired, "", ""
			m.drafts[id] = d
		} else {
			delete(m.drafts, id)
		}
		n++
	}
	return n, nil
}

func (m *Memory) PruneAudit(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.audit[:0]
	for _, r := range m.audit {
		if !r.At.Before(before) {
			kept = append(kept, r)
		}
	}
	n := int64(len(m.audit) - len(kept))
	m.audit = kept
	return n, nil
}

func (m *Memory) ListAudit(_ context.Context, owner int64, limit int) ([]AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AuditEntry
	for i := len(m.audit) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		if r := m.audit[i]; r.Owner == owner {
			out = append(out, AuditEntry{At: r.At, Event: r.Event, Detail: r.Detail})
		}
	}
	return out, nil
}

func (m *Memory) GetSubscription(_ context.Context, owner int64) (Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[owner]
	if !ok {
		return Subscription{}, ErrNotFound
	}
	return s, nil
}

func (m *Memory) SaveSubscription(_ context.Context, s Subscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs[s.OwnerID] = s
	return nil
}

func (m *Memory) AddPayment(_ context.Context, p Payment) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.payments[p.ChargeID]; dup {
		return false, nil
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	m.payments[p.ChargeID] = p
	return true, nil
}

func (m *Memory) GetPayment(_ context.Context, chargeID string) (Payment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.payments[chargeID]
	if !ok {
		return Payment{}, ErrNotFound
	}
	return p, nil
}

func (m *Memory) MarkRefunded(_ context.Context, chargeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.payments[chargeID]
	if !ok || p.Refunded {
		return ErrNotFound
	}
	p.Refunded = true
	m.payments[chargeID] = p
	return nil
}

func (m *Memory) ListSubscriptionsEnding(_ context.Context, from, to time.Time) ([]Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Subscription
	for _, s := range m.subs {
		if u := s.Until(); !u.Before(from) && u.Before(to) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OwnerID < out[j].OwnerID })
	return out, nil
}

func (m *Memory) Stats(_ context.Context, now time.Time) (Stats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var st Stats
	for _, a := range m.accounts {
		st.Accounts++
		if a.Status == StatusActive {
			st.Active++
		}
	}
	for _, s := range m.subs {
		if s.PaidUntil.After(now) {
			st.PaidActive++
		}
	}
	for _, p := range m.payments {
		if !p.Refunded {
			st.StarsTotal += int64(p.Amount)
		}
	}
	return st, nil
}

func (m *Memory) UseDaily(_ context.Context, owner int64, day string, limit int) (bool, error) {
	if _, err := time.Parse(time.DateOnly, day); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := usageKey{owner, day}
	if m.usage[k] >= limit {
		return false, nil
	}
	m.usage[k]++
	return true, nil
}

func (m *Memory) SaveGoogleLink(_ context.Context, l GoogleLink) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[l.OwnerID]; !ok {
		return ErrNotFound // Postgres'dagi FK bilan bir xil
	}
	l.CreatedAt = time.Now()
	l.Token = append([]byte(nil), l.Token...)
	m.google[l.OwnerID] = l
	return nil
}

func (m *Memory) GetGoogleLink(_ context.Context, owner int64) (GoogleLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.google[owner]
	if !ok {
		return GoogleLink{}, ErrNotFound
	}
	return l, nil
}

func (m *Memory) DeleteGoogleLink(_ context.Context, owner int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.google, owner)
	return nil
}

func (m *Memory) CreateAction(_ context.Context, a Action) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[a.OwnerID]; !ok {
		return 0, ErrNotFound
	}
	m.nextID++
	a.ID, a.Status, a.CreatedAt = m.nextID, ActionPending, time.Now()
	a.Payload = append([]byte(nil), a.Payload...)
	m.actions[a.ID] = a
	return a.ID, nil
}

func (m *Memory) GetAction(_ context.Context, owner, id int64) (Action, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.actions[id]
	if !ok || a.OwnerID != owner {
		return Action{}, ErrNotFound
	}
	return a, nil
}

func (m *Memory) SetActionStatus(_ context.Context, owner, id int64, from, to ActionStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.actions[id]
	if !ok || a.OwnerID != owner || a.Status != from {
		return ErrNotFound
	}
	a.Status = to
	m.actions[id] = a
	return nil
}

func (m *Memory) PruneActions(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for id, a := range m.actions {
		if a.CreatedAt.Before(before) {
			delete(m.actions, id)
			n++
		}
	}
	return n, nil
}

func (m *Memory) SetHook(_ context.Context, owner int64, tokenHash []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[owner]; !ok {
		return ErrNotFound
	}
	m.hooks[owner] = string(tokenHash)
	return nil
}

func (m *Memory) DeleteHook(_ context.Context, owner int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.hooks, owner)
	return nil
}

func (m *Memory) HasHook(_ context.Context, owner int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.hooks[owner]
	return ok, nil
}

func (m *Memory) OwnerByHook(_ context.Context, tokenHash []byte) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for owner, h := range m.hooks {
		if h == string(tokenHash) {
			return owner, nil
		}
	}
	return 0, ErrNotFound
}
