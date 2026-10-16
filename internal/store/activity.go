package store

import (
	"context"
	"database/sql"
	"slices"
	"sort"
	"time"
	"unicode/utf8"
)

// maxChatName — chat_activity.name uzunligi chegarasi.
const maxChatName = 64

func clipName(s string) string {
	if utf8.RuneCountInString(s) <= maxChatName {
		return s
	}
	return string([]rune(s)[:maxChatName])
}

// latest — a.LastIn va a.LastOut'ning kattasi.
func (a ChatActivity) latest() time.Time {
	if a.LastOut.After(a.LastIn) {
		return a.LastOut
	}
	return a.LastIn
}

// ---- Memory ----

func (m *Memory) ChatIn(_ context.Context, a ChatActivity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[a.OwnerID]; !ok {
		return ErrNotFound
	}
	k := [2]int64{a.OwnerID, a.ChatID}
	cur := m.chats[k]
	cur.OwnerID, cur.ChatID, cur.Private, cur.LastIn = a.OwnerID, a.ChatID, a.Private, a.LastIn
	if a.Name != "" {
		cur.Name = clipName(a.Name)
	}
	m.chats[k] = cur
	return nil
}

func (m *Memory) ChatOut(_ context.Context, owner, chatID int64, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[owner]; !ok {
		return ErrNotFound
	}
	k := [2]int64{owner, chatID}
	cur := m.chats[k]
	cur.OwnerID, cur.ChatID, cur.LastOut = owner, chatID, at
	m.chats[k] = cur
	return nil
}

func (m *Memory) ListChats(_ context.Context, owner int64, since time.Time) ([]ChatActivity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ChatActivity
	for _, a := range m.chats {
		if a.OwnerID == owner && !a.latest().Before(since) {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastIn.After(out[j].LastIn) })
	if len(out) > 200 {
		out = out[:200]
	}
	return out, nil
}

func (m *Memory) ListUnanswered(_ context.Context, before time.Time, limit int) ([]ChatActivity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ChatActivity
	for _, a := range m.chats {
		if !a.Private && a.Unanswered() && !a.LastIn.After(before) && !a.RemindedAt.After(a.LastIn) {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastIn.Before(out[j].LastIn) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) MarkChatReminded(_ context.Context, owner, chatID int64, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := [2]int64{owner, chatID}
	a, ok := m.chats[k]
	if !ok {
		return ErrNotFound
	}
	a.RemindedAt = at
	m.chats[k] = a
	return nil
}

func (m *Memory) PruneChats(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for k, a := range m.chats {
		if a.latest().Before(before) {
			delete(m.chats, k)
			n++
		}
	}
	return n, nil
}

func (m *Memory) AddStyleSample(_ context.Context, s StyleSample) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[s.OwnerID]; !ok {
		return ErrNotFound
	}
	n := 0
	for _, x := range m.samples {
		if x.OwnerID == s.OwnerID {
			n++
		}
	}
	if n >= MaxStyleSamples {
		return nil
	}
	m.sampleID++
	s.ID, s.CreatedAt = m.sampleID, time.Now()
	m.samples = append(m.samples, s)
	return nil
}

func (m *Memory) ListStyleSamples(_ context.Context, owner int64, limit int) ([]StyleSample, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []StyleSample
	for _, x := range m.samples {
		if x.OwnerID == owner && (limit <= 0 || len(out) < limit) {
			out = append(out, x)
		}
	}
	return out, nil
}

func (m *Memory) DeleteStyleSamples(_ context.Context, owner, upTo int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples = slices.DeleteFunc(m.samples, func(x StyleSample) bool { return x.OwnerID == owner && x.ID <= upTo })
	return nil
}

func (m *Memory) PruneStyleSamples(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.samples)
	m.samples = slices.DeleteFunc(m.samples, func(x StyleSample) bool { return x.CreatedAt.Before(before) })
	return int64(n - len(m.samples)), nil
}

// ---- Postgres ----

func (p *Postgres) ChatIn(ctx context.Context, a ChatActivity) error {
	_, err := p.DB.ExecContext(ctx, `
		INSERT INTO chat_activity (owner_id, chat_id, name, private, last_in) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (owner_id, chat_id) DO UPDATE SET private = EXCLUDED.private, last_in = EXCLUDED.last_in,
			name = CASE WHEN EXCLUDED.name = '' THEN chat_activity.name ELSE EXCLUDED.name END`,
		a.OwnerID, a.ChatID, clipName(a.Name), a.Private, nullTime(a.LastIn))
	return err
}

func (p *Postgres) ChatOut(ctx context.Context, owner, chatID int64, at time.Time) error {
	_, err := p.DB.ExecContext(ctx, `
		INSERT INTO chat_activity (owner_id, chat_id, last_out) VALUES ($1, $2, $3)
		ON CONFLICT (owner_id, chat_id) DO UPDATE SET last_out = EXCLUDED.last_out`,
		owner, chatID, nullTime(at))
	return err
}

const chatCols = `owner_id, chat_id, name, private, last_in, last_out, reminded_at`

func scanChats(rows *sql.Rows, err error) ([]ChatActivity, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatActivity
	for rows.Next() {
		var a ChatActivity
		var in, outT, rem sql.NullTime
		if err := rows.Scan(&a.OwnerID, &a.ChatID, &a.Name, &a.Private, &in, &outT, &rem); err != nil {
			return nil, err
		}
		a.LastIn, a.LastOut, a.RemindedAt = in.Time, outT.Time, rem.Time
		out = append(out, a)
	}
	return out, rows.Err()
}

func (p *Postgres) ListChats(ctx context.Context, owner int64, since time.Time) ([]ChatActivity, error) {
	return scanChats(p.DB.QueryContext(ctx, `SELECT `+chatCols+` FROM chat_activity
		WHERE owner_id = $1 AND (last_in >= $2 OR last_out >= $2)
		ORDER BY last_in DESC NULLS LAST LIMIT 200`, owner, since))
}

func (p *Postgres) ListUnanswered(ctx context.Context, before time.Time, limit int) ([]ChatActivity, error) {
	return scanChats(p.DB.QueryContext(ctx, `SELECT `+chatCols+` FROM chat_activity
		WHERE NOT private AND last_in IS NOT NULL AND last_in <= $1
			AND (last_out IS NULL OR last_out < last_in)
			AND (reminded_at IS NULL OR reminded_at <= last_in)
		ORDER BY last_in LIMIT $2`, before, limit))
}

func (p *Postgres) MarkChatReminded(ctx context.Context, owner, chatID int64, at time.Time) error {
	res, err := p.DB.ExecContext(ctx,
		`UPDATE chat_activity SET reminded_at = $3 WHERE owner_id = $1 AND chat_id = $2`, owner, chatID, at)
	return affected(res, err)
}

func (p *Postgres) PruneChats(ctx context.Context, before time.Time) (int64, error) {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM chat_activity
		WHERE GREATEST(COALESCE(last_in, '-infinity'), COALESCE(last_out, '-infinity')) < $1`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (p *Postgres) AddStyleSample(ctx context.Context, s StyleSample) error {
	// Chegara bitta so'rovda: parallel yozuvlar biroz oshirib yuborishi mumkin, bu zararsiz.
	_, err := p.DB.ExecContext(ctx, `
		INSERT INTO style_samples (owner_id, kind, incoming, predicted, actual)
		SELECT $1, $2, $3, $4, $5
		WHERE (SELECT count(*) FROM style_samples WHERE owner_id = $1) < $6`,
		s.OwnerID, string(s.Kind), s.Incoming, s.Predicted, s.Actual, MaxStyleSamples)
	return err
}

func (p *Postgres) ListStyleSamples(ctx context.Context, owner int64, limit int) ([]StyleSample, error) {
	if limit <= 0 {
		limit = MaxStyleSamples
	}
	rows, err := p.DB.QueryContext(ctx, `SELECT id, owner_id, kind, incoming, predicted, actual, created_at
		FROM style_samples WHERE owner_id = $1 ORDER BY id LIMIT $2`, owner, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StyleSample
	for rows.Next() {
		var s StyleSample
		var kind string
		if err := rows.Scan(&s.ID, &s.OwnerID, &kind, &s.Incoming, &s.Predicted, &s.Actual, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.Kind = SampleKind(kind)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteStyleSamples(ctx context.Context, owner, upTo int64) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM style_samples WHERE owner_id = $1 AND id <= $2`, owner, upTo)
	return err
}

func (p *Postgres) PruneStyleSamples(ctx context.Context, before time.Time) (int64, error) {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM style_samples WHERE created_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
