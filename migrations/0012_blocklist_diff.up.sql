-- Entries added / removed versus the previous ok build (0 when there was none).
ALTER TABLE blocklist_builds
    ADD COLUMN added   integer NOT NULL DEFAULT 0,
    ADD COLUMN removed integer NOT NULL DEFAULT 0;

-- The names behind those counts, so a lookup can tell when a name was listed / delisted.
CREATE TABLE blocklist_changes (
    build_id bigint  NOT NULL REFERENCES blocklist_builds(id) ON DELETE CASCADE,
    name     text    NOT NULL,  -- domain or IPv4
    added    boolean NOT NULL,  -- false = removed
    PRIMARY KEY (build_id, name)
);
CREATE INDEX blocklist_changes_name_idx ON blocklist_changes (name, build_id DESC);
