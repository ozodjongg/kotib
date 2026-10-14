-- Va'dalarni kuzatish: kotib kontaktga "keyinroq yozaman" degan chat. Xabar matni saqlanmaydi —
-- faqat kotib javobining xeshi (egasi keyin yozganini aniqlash uchun). Har chatda bittadan.
CREATE TABLE IF NOT EXISTS followups (
    owner_id   BIGINT NOT NULL REFERENCES accounts(owner_id) ON DELETE CASCADE,
    chat_id    BIGINT NOT NULL,
    hash       TEXT NOT NULL,
    due_at     TIMESTAMPTZ NOT NULL,
    status     TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','reminded','done')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_id, chat_id)
);
CREATE INDEX IF NOT EXISTS followups_due ON followups (status, due_at);
CREATE INDEX IF NOT EXISTS audit_owner_event_at ON audit (owner_id, event, at);
