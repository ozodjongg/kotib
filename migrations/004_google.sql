-- Google integratsiyasi (Calendar, Contacts, Tasks).
-- Refresh token faqat shifrlangan holda (AES-256-GCM, aad = google:<owner>).
CREATE TABLE IF NOT EXISTS google_links (
    owner_id   BIGINT PRIMARY KEY REFERENCES accounts(owner_id) ON DELETE CASCADE,
    email      TEXT NOT NULL DEFAULT '',
    token      BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Kotib taklif qilgan amallar (taqvimga qo'shish, vazifa). Egasi bot orqali tasdiqlamaguncha
-- Google'ga hech narsa yozilmaydi. Matn qisqa (sarlavha/vaqt), 7 kundan keyin o'chiriladi.
CREATE TABLE IF NOT EXISTS actions (
    id         BIGSERIAL PRIMARY KEY,
    owner_id   BIGINT NOT NULL REFERENCES accounts(owner_id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('event','task')),
    payload    JSONB NOT NULL,
    status     TEXT NOT NULL DEFAULT 'pending'
               CHECK (status IN ('pending','done','rejected','failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS actions_created ON actions (created_at);
