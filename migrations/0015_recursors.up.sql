-- Recursors (SPEC §20): PowerDNS Recursor / Unbound hosts DnsJos nodes use as upstreams,
-- installed with /install-recursor.sh and managed by dnsjos-agent in recursor mode.
CREATE TABLE recursors (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    text NOT NULL,
    kind                    text NOT NULL CHECK (kind IN ('pdns', 'unbound')),
    hostname                text NOT NULL DEFAULT '',
    public_ip               text NOT NULL DEFAULT '',
    status                  text NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending', 'online', 'degraded', 'offline')),
    settings                jsonb NOT NULL DEFAULT '{}'::jsonb,   -- api.RecursorSettings
    token_hash              bytea UNIQUE,                         -- sha256(recursor token)
    enrolled_at             timestamptz,
    last_seen_at            timestamptz,
    last_heartbeat          jsonb,                                -- latest api.RecursorHeartbeat
    agent_version           text NOT NULL DEFAULT '',
    resolver_version        text NOT NULL DEFAULT '',
    os                      text NOT NULL DEFAULT '',
    arch                    text NOT NULL DEFAULT '',
    applied_config_version  integer,
    last_error              text NOT NULL DEFAULT '',
    created_at              timestamptz NOT NULL DEFAULT now(),
    deleted_at              timestamptz
);
CREATE UNIQUE INDEX recursors_name_live_idx ON recursors(name) WHERE deleted_at IS NULL;

CREATE TABLE recursor_tokens (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash  bytea NOT NULL UNIQUE,
    name        text NOT NULL,                                    -- '' = the host's hostname
    kind        text NOT NULL CHECK (kind IN ('pdns', 'unbound')),
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    used_by     uuid REFERENCES recursors(id) ON DELETE SET NULL
);

CREATE TABLE recursor_commands (
    id            bigserial PRIMARY KEY,
    recursor_id   uuid NOT NULL REFERENCES recursors(id) ON DELETE CASCADE,
    type          text NOT NULL CHECK (type IN ('reapply', 'restart_resolver', 'upgrade_agent')),
    created_by    uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    delivered_at  timestamptz,
    acked_at      timestamptz
);
CREATE INDEX recursor_commands_pending_idx ON recursor_commands(recursor_id) WHERE acked_at IS NULL;

CREATE TABLE recursor_metrics_minutely (
    recursor_id     uuid NOT NULL REFERENCES recursors(id) ON DELETE CASCADE,
    ts              timestamptz NOT NULL,
    queries         bigint NOT NULL DEFAULT 0,
    cache_hits      bigint NOT NULL DEFAULT 0,
    cache_misses    bigint NOT NULL DEFAULT 0,
    servfail        bigint NOT NULL DEFAULT 0,
    outgoing        bigint NOT NULL DEFAULT 0,
    latency_sum_ms  double precision NOT NULL DEFAULT 0,
    samples         integer NOT NULL DEFAULT 0,
    PRIMARY KEY (recursor_id, ts)
);
CREATE INDEX recursor_metrics_minutely_ts_idx ON recursor_metrics_minutely(ts);
