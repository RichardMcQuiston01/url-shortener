CREATE TABLE link (
  id          TEXT PRIMARY KEY,
  slug        TEXT NOT NULL UNIQUE,
  target_url  TEXT NOT NULL,
  note        TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

CREATE TABLE click (
  id            TEXT PRIMARY KEY,
  link_id       TEXT NOT NULL REFERENCES link (id) ON DELETE CASCADE,
  clicked_at    TEXT NOT NULL,
  referrer_host TEXT,
  is_bot        INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX click_link_id_clicked_at_idx ON click (link_id, clicked_at);
