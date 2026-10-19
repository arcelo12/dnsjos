package recursors

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/billyriantono/dnsjos/internal/panel/app"
	"github.com/billyriantono/dnsjos/internal/panel/db"
	"github.com/billyriantono/dnsjos/internal/panel/httpx"
	"github.com/billyriantono/dnsjos/internal/shared/api"
)

// desiredFor loads a recursor and the fleet and returns its desired config.
func desiredFor(ctx context.Context, q db.Querier, id string) (api.RecursorConfig, error) {
	var kind, ip string
	var st api.RecursorSettings
	if err := q.QueryRow(ctx, "SELECT kind, public_ip, settings FROM recursors WHERE id = $1", id).
		Scan(&kind, &ip, &st); err != nil {
		return api.RecursorConfig{}, err
	}
	fleet, err := fleetAllow(ctx, q)
	if err != nil {
		return api.RecursorConfig{}, err
	}
	return desiredConfig(kind, ip, normSettings(st), fleet), nil
}

func (s *svc) config(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := desiredFor(ctx, s.d.Pool, app.RecursorIDFrom(ctx))
	if err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	etag := `"` + strconv.Itoa(c.Version) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if httpx.ETagMatch(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	set := s.d.Settings.Get()
	c.PollIntervalS, c.HeartbeatIntervalS = set.AgentPollIntervalS, set.AgentHeartbeatIntervalS
	httpx.WriteJSON(w, http.StatusOK, c)
}

func recursorStatus(hb *api.RecursorHeartbeat) string {
	if !hb.ResolverRunning || !hb.ProbeOK || hb.ApplyError != "" {
		return api.NodeDegraded
	}
	return api.NodeOnline
}

// counterDelta turns raw resolver counters into increments: no baseline → zero, any
// counter going down (resolver restart) → the new values are the increments.
func counterDelta(prev *api.RecursorCounters, cur api.RecursorCounters) api.RecursorCounters {
	if prev == nil {
		return api.RecursorCounters{}
	}
	if cur.Queries < prev.Queries || cur.CacheHits < prev.CacheHits || cur.CacheMisses < prev.CacheMisses ||
		cur.Servfail < prev.Servfail || cur.Outgoing < prev.Outgoing {
		return cur
	}
	return api.RecursorCounters{Queries: cur.Queries - prev.Queries, CacheHits: cur.CacheHits - prev.CacheHits,
		CacheMisses: cur.CacheMisses - prev.CacheMisses, Servfail: cur.Servfail - prev.Servfail,
		Outgoing: cur.Outgoing - prev.Outgoing}
}

// gapMinutes is the number of minute buckets a delta covering gap is spread over (1 for
// normal heartbeats; a panel outage's traffic does not land as one spike). Capped at a week.
func gapMinutes(gap time.Duration) int {
	n := int((gap + time.Minute - 1) / time.Minute)
	return max(1, min(n, 7*24*60))
}

// metricsInsert adds a delta to recursor_metrics_minutely, spread evenly over the $9
// minutes ending at the current one (remainders to the newest; latency only in the current).
const metricsInsert = `
	INSERT INTO recursor_metrics_minutely (recursor_id, ts, queries, cache_hits, cache_misses, servfail, outgoing,
		latency_sum_ms, samples)
	SELECT $1, date_trunc('minute', now()) - make_interval(mins => k),
		$2::bigint / $9 + (k < $2::bigint % $9)::int, $3::bigint / $9 + (k < $3::bigint % $9)::int,
		$4::bigint / $9 + (k < $4::bigint % $9)::int, $5::bigint / $9 + (k < $5::bigint % $9)::int,
		$6::bigint / $9 + (k < $6::bigint % $9)::int,
		CASE WHEN k = 0 THEN $7::double precision ELSE 0 END, CASE WHEN k = 0 THEN $8::int ELSE 0 END
	FROM generate_series(0, $9::int - 1) AS k
	ON CONFLICT (recursor_id, ts) DO UPDATE SET
		queries = recursor_metrics_minutely.queries + EXCLUDED.queries,
		cache_hits = recursor_metrics_minutely.cache_hits + EXCLUDED.cache_hits,
		cache_misses = recursor_metrics_minutely.cache_misses + EXCLUDED.cache_misses,
		servfail = recursor_metrics_minutely.servfail + EXCLUDED.servfail,
		outgoing = recursor_metrics_minutely.outgoing + EXCLUDED.outgoing,
		latency_sum_ms = recursor_metrics_minutely.latency_sum_ms + EXCLUDED.latency_sum_ms,
		samples = recursor_metrics_minutely.samples + EXCLUDED.samples`

func (s *svc) heartbeat(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), app.RecursorIDFrom(r.Context())
	var hb api.RecursorHeartbeat
	if err := httpx.ReadJSON(r, &hb, httpx.MaxJSON); err != nil {
		httpx.BadRequest(w, err.Error())
		return
	}
	if len(hb.AckedCommands) > 1000 {
		httpx.BadRequest(w, "too many acked commands")
		return
	}
	hb.ListenIP, hb.ListenIPv6 = clip(hb.ListenIP, 64), clip(hb.ListenIPv6, 64)
	raw, err := json.Marshal(hb)
	if err != nil {
		httpx.BadRequest(w, err.Error())
		return
	}
	var ack api.RecursorAck
	e := liveEntry{hb: hb, at: time.Now()}
	err = pgx.BeginFunc(ctx, s.d.Pool, func(tx pgx.Tx) error {
		var prevRaw []byte
		// FOR UPDATE serialises concurrent heartbeats of one recursor (delta math).
		if err := tx.QueryRow(ctx, "SELECT last_heartbeat FROM recursors WHERE id = $1 FOR UPDATE", id).Scan(&prevRaw); err != nil {
			return err
		}
		var prev *api.RecursorHeartbeat
		if prevRaw != nil {
			prev = new(api.RecursorHeartbeat)
			// Without stats (resolver down, stats error) a heartbeat is no baseline.
			if json.Unmarshal(prevRaw, prev) != nil || !prev.ResolverRunning || prev.StatsError != "" {
				prev = nil
			}
		}
		var pc *api.RecursorCounters
		if prev != nil {
			pc = &prev.Counters
		}
		d := counterDelta(pc, hb.Counters)
		if hb.StatsError != "" || !hb.ResolverRunning {
			d = api.RecursorCounters{}
		}
		// An older heartbeat whose counters went down is a late delivery, not a restart.
		if prev != nil && hb.Time.Before(prev.Time) && d == hb.Counters && d != (api.RecursorCounters{}) {
			d, prev, raw = api.RecursorCounters{}, nil, prevRaw
		}
		if prev != nil {
			if dt := hb.Time.Sub(prev.Time).Seconds(); dt > 0 && dt <= 300 {
				e.qps = float64(d.Queries) / dt
				if n := d.CacheHits + d.CacheMisses; n > 0 {
					e.hit = float64(d.CacheHits) / float64(n)
				}
			}
		}
		errs := []string{}
		for _, m := range []string{hb.ApplyError, hb.ProbeError, hb.StatsError} {
			if m != "" {
				errs = append(errs, m)
			}
		}
		if !hb.ResolverRunning {
			errs = append(errs, "the resolver service is not running")
		}
		applied := &hb.AppliedConfigVersion
		if hb.AppliedConfigVersion == 0 {
			applied = nil
		}
		if _, err := tx.Exec(ctx, `UPDATE recursors SET last_seen_at = now(), last_heartbeat = $2, status = $3,
			agent_version = $4, resolver_version = $5, os = $6, applied_config_version = $7, last_error = $8
			WHERE id = $1`, id, raw, recursorStatus(&hb), clip(hb.AgentVersion, 64), clip(hb.ResolverVersion, 128),
			clip(hb.OS, 128), applied, clip(strings.Join(errs, "; "), 4000)); err != nil {
			return err
		}
		samples, latency := 0, 0.0
		if d.Queries > 0 && hb.LatencyAvgMs > 0 {
			samples, latency = 1, hb.LatencyAvgMs
		}
		if d != (api.RecursorCounters{}) || samples > 0 {
			spread := 1
			if prev != nil {
				spread = gapMinutes(hb.Time.Sub(prev.Time))
			}
			if _, err := tx.Exec(ctx, metricsInsert, id, d.Queries, d.CacheHits, d.CacheMisses, d.Servfail, d.Outgoing,
				latency, samples, spread); err != nil {
				return err
			}
		}
		if len(hb.AckedCommands) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE recursor_commands SET acked_at = now()
				WHERE recursor_id = $1 AND id = ANY($2) AND acked_at IS NULL`, id, hb.AckedCommands); err != nil {
				return err
			}
		}
		rows, _ := tx.Query(ctx, `UPDATE recursor_commands SET delivered_at = now()
			WHERE recursor_id = $1 AND delivered_at IS NULL RETURNING id, type`, id)
		cmds, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (api.Command, error) {
			var c api.Command
			err := row.Scan(&c.ID, &c.Type)
			return c, err
		})
		if err != nil {
			return err
		}
		ack.Commands = cmds
		c, err := desiredFor(ctx, tx, id)
		ack.ConfigVersion = c.Version
		return err
	})
	if err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	s.mu.Lock()
	s.live[id] = e
	s.mu.Unlock()
	httpx.WriteJSON(w, http.StatusOK, ack)
}

// statusTick marks recursors offline after 3 missed heartbeats.
func (s *svc) statusTick(ctx context.Context) error {
	secs := 3 * s.d.Settings.Get().AgentHeartbeatIntervalS
	_, err := s.d.Pool.Exec(ctx, `UPDATE recursors SET status = 'offline'
		WHERE deleted_at IS NULL AND status IN ('online', 'degraded')
		  AND last_seen_at < now() - make_interval(secs => $1)`, secs)
	return err
}
