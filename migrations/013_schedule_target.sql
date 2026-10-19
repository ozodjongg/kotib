-- Rejalashtirilgan vazifa natijasini egasining kontaktiga yuborish (0 — faqat egasiga, bot chatiga).
-- target_auto: true — tasdiqsiz yuboriladi (filtr va limitdan o'tsa), false — egasiga qoralama.
ALTER TABLE schedules ADD COLUMN IF NOT EXISTS target_id   BIGINT NOT NULL DEFAULT 0;
ALTER TABLE schedules ADD COLUMN IF NOT EXISTS target_name TEXT NOT NULL DEFAULT '';
ALTER TABLE schedules ADD COLUMN IF NOT EXISTS target_auto BOOLEAN NOT NULL DEFAULT false;
