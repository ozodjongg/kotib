package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
)

// Admin paneli: foydalanuvchilar ro'yxati, bloklash va to'lovlar.

// AdminEvents — admin ro'yxatida sanaladigan hodisalar (foydalanish va xavf belgilari).
var AdminEvents = []string{
	"draft_created", "auto_sent", "draft_sent", "draft_rejected", "skip",
	"blocked", "output_rejected", "llm_error", "flood_wait", "circuit_breaker", "fatal",
}

// Ban — admin bloklagan foydalanuvchi.
type Ban struct {
	OwnerID int64
	Reason  string
	By      int64 // bloklagan admin
	At      time.Time
}

// AccountRow — admin ro'yxatidagi bitta akkaunt.
type AccountRow struct {
	Account
	Settings    config.Settings // sozlama saqlanmagan bo'lsa — config.Default()
	Ban         *Ban            // nil — bloklanmagan
	LastEventAt *time.Time      // oxirgi audit hodisasi
	Counts      map[string]int  // since'dan keyingi AdminEvents soni
}

// ---- Memory ----

func (m *Memory) TouchAccount(_ context.Context, owner int64, name, username string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[owner]
	if !ok {
		return nil
	}
	a.Name, a.Username, a.SeenAt = name, username, &at
	m.accounts[owner] = a
	return nil
}

func (m *Memory) ListAccounts(_ context.Context, since time.Time, limit int) ([]AccountRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]AccountRow, 0, len(m.accounts))
	idx := map[int64]int{}
	for id, a := range m.accounts {
		r := AccountRow{Account: a, Settings: config.Default(), Counts: map[string]int{}}
		if st, ok := m.settings[id]; ok {
			r.Settings = st
		}
		if b, ok := m.bans[id]; ok {
			r.Ban = &b
		}
		idx[id] = len(rows)
		rows = append(rows, r)
	}
	for _, e := range m.audit {
		i, ok := idx[e.Owner]
		if !ok {
			continue
		}
		r := &rows[i]
		if r.LastEventAt == nil || e.At.After(*r.LastEventAt) {
			at := e.At
			r.LastEventAt = &at
		}
		if !e.At.Before(since) && isAdminEvent(e.Event) {
			r.Counts[e.Event]++
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		}
		return rows[i].OwnerID > rows[j].OwnerID
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func isAdminEvent(e string) bool {
	for _, x := range AdminEvents {
		if x == e {
			return true
		}
	}
	return false
}

func (m *Memory) ListPayments(_ context.Context, limit int) ([]Payment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Payment, 0, len(m.payments))
	for _, p := range m.payments {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) SetBan(_ context.Context, b Ban) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b.At.IsZero() {
		b.At = time.Now()
	}
	m.bans[b.OwnerID] = b
	return nil
}

func (m *Memory) GetBan(_ context.Context, owner int64) (Ban, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.bans[owner]
	if !ok {
		return Ban{}, ErrNotFound
	}
	return b, nil
}

func (m *Memory) DeleteBan(_ context.Context, owner int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.bans, owner)
	return nil
}

// ---- Postgres ----

func (p *Postgres) TouchAccount(ctx context.Context, owner int64, name, username string, at time.Time) error {
	_, err := p.DB.ExecContext(ctx,
		// Mini App har 20 soniyada /api/me so'raydi: o'zgarmagan bo'lsa 5 daqiqada bir martadan ko'p yozilmaydi.
		`UPDATE accounts SET tg_name = $2, tg_username = $3, seen_at = $4
		WHERE owner_id = $1 AND (seen_at IS NULL OR seen_at < $4::timestamptz - interval '5 minutes' OR tg_name <> $2 OR tg_username <> $3)`,
		owner, name, username, at)
	return err
}

func (p *Postgres) ListAccounts(ctx context.Context, since time.Time, limit int) ([]AccountRow, error) {
	if limit <= 0 {
		limit = 10000
	}
	rows, err := p.DB.QueryContext(ctx, `
		SELECT a.owner_id, a.status, a.status_note, a.consent_at, a.created_at, a.tg_name, a.tg_username, a.seen_at,
			s.data, b.reason, b.by_admin, b.created_at,
			(SELECT max(x.at) FROM audit x WHERE x.owner_id = a.owner_id)
		FROM accounts a
		LEFT JOIN settings s ON s.owner_id = a.owner_id
		LEFT JOIN bans b ON b.owner_id = a.owner_id
		ORDER BY a.created_at DESC, a.owner_id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccountRow
	idx := map[int64]int{}
	for rows.Next() {
		var r AccountRow
		var st string
		var consent, seen, banAt, last sql.NullTime
		var data []byte
		var reason sql.NullString
		var by sql.NullInt64
		if err := rows.Scan(&r.OwnerID, &st, &r.StatusNote, &consent, &r.CreatedAt, &r.Name, &r.Username, &seen,
			&data, &reason, &by, &banAt, &last); err != nil {
			return nil, err
		}
		r.Status = AccountStatus(st)
		r.ConsentAt, r.SeenAt, r.LastEventAt = timePtr(consent), timePtr(seen), timePtr(last)
		r.Settings = config.Default()
		if len(data) > 0 {
			if err := json.Unmarshal(data, &r.Settings); err != nil {
				return nil, err
			}
		}
		if by.Valid {
			r.Ban = &Ban{OwnerID: r.OwnerID, Reason: reason.String, By: by.Int64, At: banAt.Time}
		}
		r.Counts = map[string]int{}
		idx[r.OwnerID] = len(out)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	crows, err := p.DB.QueryContext(ctx,
		`SELECT owner_id, event, count(*) FROM audit WHERE at >= $1 AND event = ANY($2) GROUP BY owner_id, event`,
		since, AdminEvents)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var owner int64
		var ev string
		var n int
		if err := crows.Scan(&owner, &ev, &n); err != nil {
			return nil, err
		}
		if i, ok := idx[owner]; ok {
			out[i].Counts[ev] = n
		}
	}
	return out, crows.Err()
}

func timePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func (p *Postgres) ListPayments(ctx context.Context, limit int) ([]Payment, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := p.DB.QueryContext(ctx, `
		SELECT charge_id, owner_id, amount, currency, payload, recurring, refunded, created_at
		FROM payments ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Payment
	for rows.Next() {
		var x Payment
		if err := rows.Scan(&x.ChargeID, &x.OwnerID, &x.Amount, &x.Currency, &x.Payload, &x.Recurring, &x.Refunded, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (p *Postgres) SetBan(ctx context.Context, b Ban) error {
	_, err := p.DB.ExecContext(ctx, `
		INSERT INTO bans (owner_id, reason, by_admin, created_at) VALUES ($1, $2, $3, now())
		ON CONFLICT (owner_id) DO UPDATE SET reason = EXCLUDED.reason, by_admin = EXCLUDED.by_admin, created_at = now()`,
		b.OwnerID, b.Reason, b.By)
	return err
}

func (p *Postgres) GetBan(ctx context.Context, owner int64) (Ban, error) {
	b := Ban{OwnerID: owner}
	err := p.DB.QueryRowContext(ctx, `SELECT reason, by_admin, created_at FROM bans WHERE owner_id = $1`, owner).
		Scan(&b.Reason, &b.By, &b.At)
	if errors.Is(err, sql.ErrNoRows) {
		return Ban{}, ErrNotFound
	}
	return b, err
}

func (p *Postgres) DeleteBan(ctx context.Context, owner int64) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM bans WHERE owner_id = $1`, owner)
	return err
}
