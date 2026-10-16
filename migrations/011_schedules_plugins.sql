-- Rejalashtirilgan vazifalar: egasi vaqt va AI ko'rsatmasini (prompt) yozadi, kotib o'sha vaqtda
-- bajarib natijani bot chatiga yuboradi. kind: once (date + at), hourly (at'ning daqiqasi),
-- daily, weekdays (Du–Ju), weekly (weekdays bitmaski: 1<<0 yakshanba ... 1<<6 shanba).
-- next_run_at — keyingi bajarilish (UTC); NULL — o'chiq yoki bir martalik bajarilgan.
CREATE TABLE IF NOT EXISTS schedules (
    id          BIGSERIAL PRIMARY KEY,
    owner_id    BIGINT NOT NULL REFERENCES accounts(owner_id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    prompt      TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('once','hourly','daily','weekdays','weekly')),
    at          TEXT NOT NULL,               -- "HH:MM" (egasining mintaqasida)
    date        TEXT NOT NULL DEFAULT '',    -- "YYYY-MM-DD" (faqat once)
    weekdays    INT NOT NULL DEFAULT 0,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    next_run_at TIMESTAMPTZ,
    last_run_at TIMESTAMPTZ,
    last_status TEXT NOT NULL DEFAULT '',    -- '' | running | ok | error
    last_result TEXT NOT NULL DEFAULT '',
    runs        INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS schedules_owner ON schedules (owner_id, id);
CREATE INDEX IF NOT EXISTS schedules_due ON schedules (next_run_at) WHERE enabled;

-- Plaginlar (connector'lar): egasida yoqilgan plagin va sozlamasi. config — shifrlangan JSON
-- (API kalitlari ham shu yerda, crypto.Vault bilan; AAD — egasi va plagin ID'si).
CREATE TABLE IF NOT EXISTS plugin_configs (
    owner_id   BIGINT NOT NULL REFERENCES accounts(owner_id) ON DELETE CASCADE,
    plugin_id  TEXT NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT true,
    config     BYTEA,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_id, plugin_id)
);
