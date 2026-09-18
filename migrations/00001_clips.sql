-- The lines this plugin has spoken, one row each, audio included.
--
-- The host creates the schema and runs this as a role that owns it and nothing
-- else, so everything here is unqualified. There is no down migration: removing
-- the plugin drops the schema whole.
CREATE TABLE clips (
    -- Everything that changes the audio, hashed: model, voice, language,
    -- format, speed and the words. The same line in the same voice is one row.
    key          text        PRIMARY KEY,
    text         text        NOT NULL,
    model        text        NOT NULL,
    voice_id     text        NOT NULL,
    language     text        NOT NULL DEFAULT '',
    container    text        NOT NULL,
    sample_rate  integer     NOT NULL,
    -- What the vendor was billed for, once.
    characters   integer     NOT NULL,
    audio        bytea       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    -- The retention setting keeps what is still being asked for.
    last_used_at timestamptz NOT NULL DEFAULT now(),
    hits         integer     NOT NULL DEFAULT 0
);

CREATE INDEX clips_last_used ON clips (last_used_at);
CREATE INDEX clips_created ON clips (created_at DESC);
