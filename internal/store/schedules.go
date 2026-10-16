package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"
)

// staleRun — "running" holati shundan eski bo'lsa (jarayon to'xtagan), vazifani qayta ishga tushirish mumkin.
const staleRun = 10 * time.Minute

func runningNow(s Schedule, at time.Time) bool {
	return s.LastStatus == "running" && s.LastRun != nil && at.Sub(*s.LastRun) < staleRun
}

func usec(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) } // Postgres aniqligi

func usecPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := usec(*t)
	return &v
}

// ---- Memory ----

func (m *Memory) CreateSchedule(_ context.Context, s Schedule) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[s.OwnerID]; !ok {
		return 0, ErrNotFound
	}
	if m.schedules == nil {
		m.schedules = map[int64]Schedule{}
	}
	m.nextID++
	s.ID, s.CreatedAt, s.NextRun = m.nextID, usec(time.Now()), usecPtr(s.NextRun)
	s.LastRun, s.LastStatus, s.LastResult, s.Runs = nil, "", "", 0
	m.schedules[s.ID] = s
	return s.ID, nil
}

func (m *Memory) GetSchedule(_ context.Context, owner, id int64) (Schedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.schedules[id]
	if !ok || s.OwnerID != owner {
		return Schedule{}, ErrNotFound
	}
	return s, nil
}

func (m *Memory) ListSchedules(_ context.Context, owner int64) ([]Schedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Schedule
	for _, s := range m.schedules {
		if s.OwnerID == owner {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b Schedule) int { return int(a.ID - b.ID) })
	return out, nil
}

func (m *Memory) UpdateSchedule(_ context.Context, s Schedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.schedules[s.ID]
	if !ok || cur.OwnerID != s.OwnerID {
		return ErrNotFound
	}
	cur.Title, cur.Prompt, cur.Kind, cur.At, cur.Date, cur.Weekdays = s.Title, s.Prompt, s.Kind, s.At, s.Date, s.Weekdays
	cur.Enabled, cur.NextRun = s.Enabled, usecPtr(s.NextRun)
	m.schedules[s.ID] = cur
	return nil
}

func (m *Memory) DeleteSchedule(_ context.Context, owner, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.schedules[id]; !ok || s.OwnerID != owner {
		return ErrNotFound
	}
	delete(m.schedules, id)
	return nil
}

func (m *Memory) ListDueSchedules(_ context.Context, before time.Time, limit int) ([]Schedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Schedule
	for _, s := range m.schedules {
		if s.Enabled && s.NextRun != nil && !s.NextRun.After(before) {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b Schedule) int { return a.NextRun.Compare(*b.NextRun) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) ClaimSchedule(_ context.Context, owner, id int64, expected time.Time, next *time.Time, at time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.schedules[id]
	if !ok || s.OwnerID != owner || !s.Enabled || s.NextRun == nil || !s.NextRun.Equal(usec(expected)) {
		return false, nil
	}
	s.NextRun, s.Enabled = usecPtr(next), next != nil
	at = usec(at)
	s.LastRun, s.LastStatus = &at, "running"
	m.schedules[id] = s
	return true, nil
}

func (m *Memory) MarkScheduleRunning(_ context.Context, owner, id int64, at time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.schedules[id]
	if !ok || s.OwnerID != owner {
		return false, ErrNotFound
	}
	if runningNow(s, at) {
		return false, nil
	}
	at = usec(at)
	s.LastRun, s.LastStatus = &at, "running"
	m.schedules[id] = s
	return true, nil
}

func (m *Memory) FinishSchedule(_ context.Context, owner, id int64, status, result string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.schedules[id]
	if !ok || s.OwnerID != owner {
		return ErrNotFound
	}
	s.LastStatus, s.LastResult = status, result
	s.Runs++
	m.schedules[id] = s
	return nil
}

func (m *Memory) ListPluginConfigs(_ context.Context, owner int64) ([]PluginConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []PluginConfig
	for k, c := range m.plugins {
		if k.owner == owner {
			c.Config = slices.Clone(c.Config)
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b PluginConfig) int {
		switch {
		case a.PluginID < b.PluginID:
			return -1
		case a.PluginID > b.PluginID:
			return 1
		}
		return 0
	})
	return out, nil
}

func (m *Memory) SavePluginConfig(_ context.Context, c PluginConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[c.OwnerID]; !ok {
		return ErrNotFound
	}
	if m.plugins == nil {
		m.plugins = map[pluginKey]PluginConfig{}
	}
	c.Config, c.UpdatedAt = slices.Clone(c.Config), time.Now()
	m.plugins[pluginKey{c.OwnerID, c.PluginID}] = c
	return nil
}

func (m *Memory) DeletePluginConfig(_ context.Context, owner int64, pluginID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.plugins, pluginKey{owner, pluginID})
	return nil
}

type pluginKey struct {
	owner int64
	id    string
}

// deleteOwnerSchedules — DeleteAccount uchun (m.mu ushlangan).
func (m *Memory) deleteOwnerSchedules(owner int64) {
	for id, s := range m.schedules {
		if s.OwnerID == owner {
			delete(m.schedules, id)
		}
	}
	for k := range m.plugins {
		if k.owner == owner {
			delete(m.plugins, k)
		}
	}
}

// ---- Postgres ----

const scheduleCols = `id, owner_id, title, prompt, kind, at, date, weekdays, enabled, next_run_at, last_run_at,
	last_status, last_result, runs, created_at`

func scanSchedule(sc interface{ Scan(...any) error }) (Schedule, error) {
	var s Schedule
	var kind string
	var next, last sql.NullTime
	err := sc.Scan(&s.ID, &s.OwnerID, &s.Title, &s.Prompt, &kind, &s.At, &s.Date, &s.Weekdays, &s.Enabled,
		&next, &last, &s.LastStatus, &s.LastResult, &s.Runs, &s.CreatedAt)
	if err != nil {
		return s, err
	}
	s.Kind = ScheduleKind(kind)
	if next.Valid {
		t := next.Time
		s.NextRun = &t
	}
	if last.Valid {
		t := last.Time
		s.LastRun = &t
	}
	return s, nil
}

func (p *Postgres) listSchedules(ctx context.Context, q string, args ...any) ([]Schedule, error) {
	rows, err := p.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func ptrNull(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

func (p *Postgres) CreateSchedule(ctx context.Context, s Schedule) (int64, error) {
	var id int64
	err := p.DB.QueryRowContext(ctx, `
		INSERT INTO schedules (owner_id, title, prompt, kind, at, date, weekdays, enabled, next_run_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		s.OwnerID, s.Title, s.Prompt, string(s.Kind), s.At, s.Date, s.Weekdays, s.Enabled, ptrNull(s.NextRun)).Scan(&id)
	return id, err
}

func (p *Postgres) GetSchedule(ctx context.Context, owner, id int64) (Schedule, error) {
	s, err := scanSchedule(p.DB.QueryRowContext(ctx, `SELECT `+scheduleCols+` FROM schedules WHERE id = $1 AND owner_id = $2`, id, owner))
	if errors.Is(err, sql.ErrNoRows) {
		return Schedule{}, ErrNotFound
	}
	return s, err
}

func (p *Postgres) ListSchedules(ctx context.Context, owner int64) ([]Schedule, error) {
	return p.listSchedules(ctx, `SELECT `+scheduleCols+` FROM schedules WHERE owner_id = $1 ORDER BY id`, owner)
}

func (p *Postgres) UpdateSchedule(ctx context.Context, s Schedule) error {
	res, err := p.DB.ExecContext(ctx, `
		UPDATE schedules SET title = $3, prompt = $4, kind = $5, at = $6, date = $7, weekdays = $8, enabled = $9, next_run_at = $10
		WHERE id = $1 AND owner_id = $2`,
		s.ID, s.OwnerID, s.Title, s.Prompt, string(s.Kind), s.At, s.Date, s.Weekdays, s.Enabled, ptrNull(s.NextRun))
	return affected(res, err)
}

func (p *Postgres) DeleteSchedule(ctx context.Context, owner, id int64) error {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM schedules WHERE id = $1 AND owner_id = $2`, id, owner)
	return affected(res, err)
}

func (p *Postgres) ListDueSchedules(ctx context.Context, before time.Time, limit int) ([]Schedule, error) {
	return p.listSchedules(ctx, `SELECT `+scheduleCols+` FROM schedules
		WHERE enabled AND next_run_at <= $1 ORDER BY next_run_at LIMIT $2`, before, limit)
}

func (p *Postgres) ClaimSchedule(ctx context.Context, owner, id int64, expected time.Time, next *time.Time, at time.Time) (bool, error) {
	res, err := p.DB.ExecContext(ctx, `
		UPDATE schedules SET next_run_at = $4, enabled = $5, last_run_at = $6, last_status = 'running'
		WHERE id = $1 AND owner_id = $2 AND enabled AND next_run_at = $3`,
		id, owner, expected, ptrNull(next), next != nil, at)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (p *Postgres) MarkScheduleRunning(ctx context.Context, owner, id int64, at time.Time) (bool, error) {
	res, err := p.DB.ExecContext(ctx, `
		UPDATE schedules SET last_run_at = $3, last_status = 'running'
		WHERE id = $1 AND owner_id = $2 AND NOT (last_status = 'running' AND last_run_at > $4)`,
		id, owner, at, at.Add(-staleRun))
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return true, nil
	}
	if _, err := p.GetSchedule(ctx, owner, id); err != nil {
		return false, err
	}
	return false, nil
}

func (p *Postgres) FinishSchedule(ctx context.Context, owner, id int64, status, result string) error {
	res, err := p.DB.ExecContext(ctx, `
		UPDATE schedules SET last_status = $3, last_result = $4, runs = runs + 1 WHERE id = $1 AND owner_id = $2`,
		id, owner, status, result)
	return affected(res, err)
}

func (p *Postgres) ListPluginConfigs(ctx context.Context, owner int64) ([]PluginConfig, error) {
	rows, err := p.DB.QueryContext(ctx,
		`SELECT owner_id, plugin_id, enabled, config, updated_at FROM plugin_configs WHERE owner_id = $1 ORDER BY plugin_id`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PluginConfig
	for rows.Next() {
		var c PluginConfig
		if err := rows.Scan(&c.OwnerID, &c.PluginID, &c.Enabled, &c.Config, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (p *Postgres) SavePluginConfig(ctx context.Context, c PluginConfig) error {
	_, err := p.DB.ExecContext(ctx, `
		INSERT INTO plugin_configs (owner_id, plugin_id, enabled, config, updated_at) VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (owner_id, plugin_id) DO UPDATE SET enabled = EXCLUDED.enabled, config = EXCLUDED.config, updated_at = now()`,
		c.OwnerID, c.PluginID, c.Enabled, c.Config)
	return err
}

func (p *Postgres) DeletePluginConfig(ctx context.Context, owner int64, pluginID string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM plugin_configs WHERE owner_id = $1 AND plugin_id = $2`, owner, pluginID)
	return err
}
