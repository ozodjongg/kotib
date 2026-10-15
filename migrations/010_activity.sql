-- Chat faolligi (xabar matnisiz): kunlik AI xulosa uchun bugun kim yozgani va javobsiz qolgan
-- chatlar eslatmasi. Private — egasi qoidalar bilan kotibni chetlatgan chat: AI xulosaga ham,
-- eslatmalarga ham kirmaydi.
CREATE TABLE IF NOT EXISTS chat_activity (
    owner_id    BIGINT NOT NULL REFERENCES accounts(owner_id) ON DELETE CASCADE,
    chat_id     BIGINT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    private     BOOLEAN NOT NULL DEFAULT false,
    last_in     TIMESTAMPTZ,
    last_out    TIMESTAMPTZ,
    reminded_at TIMESTAMPTZ,
    PRIMARY KEY (owner_id, chat_id)
);
CREATE INDEX IF NOT EXISTS chat_activity_in ON chat_activity (last_in);

-- O'qitish rejimi: kotib taxmini va egasi haqiqatda yozgan javob juftligi (soya rejimi yoki
-- tahrirlangan qoralama). Uslub profilini yangilashda ishlatiladi va o'chiriladi; 7 kundan
-- eski yozuvlarni janitor tozalaydi (draftlar kabi).
CREATE TABLE IF NOT EXISTS style_samples (
    id         BIGSERIAL PRIMARY KEY,
    owner_id   BIGINT NOT NULL REFERENCES accounts(owner_id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('shadow','edit')),
    incoming   TEXT NOT NULL DEFAULT '',
    predicted  TEXT NOT NULL,
    actual     TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS style_samples_owner ON style_samples (owner_id, id);
