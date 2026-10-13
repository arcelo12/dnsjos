-- Blocklists per profile (SPEC §7.1). A blocklist source and the CDB built from it may
-- belong to a config profile; profile_id IS NULL keeps the source global, shared by every
-- profile, and a profile with no source of its own falls back to the global build.

ALTER TABLE blocklist_sources
    ADD COLUMN profile_id uuid REFERENCES config_profiles(id) ON DELETE CASCADE;

-- A URL or manual list can be reused by several profiles: two rows may name the same URL
-- once per profile. The old rows have no profile and stay global.
CREATE INDEX blocklist_sources_profile_idx ON blocklist_sources (profile_id);

ALTER TABLE blocklist_builds
    ADD COLUMN profile_id uuid REFERENCES config_profiles(id) ON DELETE CASCADE;

-- The newest ok build is looked up per profile, so the index has to carry the profile.
DROP INDEX IF EXISTS blocklist_builds_ok_idx;
CREATE INDEX blocklist_builds_ok_idx ON blocklist_builds (profile_id, finished_at DESC) WHERE status = 'ok';
