-- PostgreSQL 15+
CREATE TABLE IF NOT EXISTS accounts (
    owner_id    BIGINT PRIMARY KEY,
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending','active','paused','broken','revoked')),
    status_note TEXT NOT NULL DEFAULT '',
    consent_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Faqat shifrlangan blob (AES-256-GCM, envelope). Ochiq session hech qachon saqlanmaydi.
CREATE TABLE IF NOT EXISTS sessions (
    owner_id   BIGINT PRIMARY KEY REFERENCES accounts(owner_id) ON DELETE CASCADE,
    blob       BYTEA NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS settings (
    owner_id   BIGINT PRIMARY KEY REFERENCES accounts(owner_id) ON DELETE CASCADE,
    data       JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS drafts (
    id          BIGSERIAL PRIMARY KEY,
    owner_id    BIGINT NOT NULL REFERENCES accounts(owner_id) ON DELETE CASCADE,
    chat_id     BIGINT NOT NULL,
    reply_to    INT NOT NULL DEFAULT 0,
    incoming    TEXT NOT NULL,
    reply       TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending','sent','rejected','expired')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS drafts_owner_status ON drafts (owner_id, status, id DESC);

-- Audit: xabar matni YO'Q, faqat hodisa va meta.
CREATE TABLE IF NOT EXISTS audit (
    id        BIGSERIAL PRIMARY KEY,
    owner_id  BIGINT NOT NULL REFERENCES accounts(owner_id) ON DELETE CASCADE,
    at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    event     TEXT NOT NULL,
    detail    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS audit_owner_at ON audit (owner_id, at DESC);
CREATE INDEX IF NOT EXISTS audit_at ON audit (at);
CREATE INDEX IF NOT EXISTS drafts_created ON drafts (created_at);

-- Draft'larni uzoq saqlamaslik (maxfiylik): ilova o'zi har soatda 7 kundan eski
-- draftlarni "expired" qiladi/o'chiradi va 90 kundan eski auditni tozalaydi (cmd/secretary janitor).
