-- SPEC §6.9: each node's fastest-IP speed-check measurements (latest report per node).
ALTER TABLE nodes
    ADD COLUMN speedcheck         jsonb NOT NULL DEFAULT '[]'::jsonb,  -- []api.SpeedCheckName, measured names only
    ADD COLUMN speedcheck_checked integer NOT NULL DEFAULT 0,
    ADD COLUMN speedcheck_at      timestamptz;
