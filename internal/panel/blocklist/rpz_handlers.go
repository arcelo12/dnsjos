package blocklist

// HTTP handlers for the RPZ slave's upstreams (SPEC §7.7). An upstream always owns exactly
// one blocklist source of kind rpz_domains: creating an upstream creates the paired source
// in the same transaction, and deleting the upstream cascades to it (FK ON DELETE CASCADE).

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/miekg/dns"

	"github.com/billyriantono/dnsjos/internal/panel/audit"
	"github.com/billyriantono/dnsjos/internal/panel/db"
	"github.com/billyriantono/dnsjos/internal/panel/httpx"
	"github.com/billyriantono/dnsjos/internal/shared/api"
)

// dnsZone canonicalises a zone name to FQDN form (trailing dot) for storage.
func dnsZone(z string) string { return dns.Fqdn(z) }

// rpzColsFull is rpzCols plus the presentation fields (created/updated, profile name) a
// CRUD read returns. The sync job uses the narrower rpzCols.
const rpzFullCols = rpzCols + `, coalesce((SELECT name FROM config_profiles WHERE id = s.profile_id), ''),
	u.last_sync_at, u.created_at, u.updated_at`

func scanRPZFull(row pgx.Row) (api.RPZUpstream, error) {
	var (
		u       rpzUpstream
		prof    string
		last    *time.Time
		cre, u2 time.Time
	)
	err := row.Scan(&u.ID, &u.Name, &u.Upstream, &u.Zone, &u.TSIGKey, &u.TSIGSecret,
		&u.IntervalS, &u.Serial, &u.TotalDomains, &u.LastError, &u.SourceID, &u.ProfileID,
		&prof, &last, &cre, &u2)
	if err != nil {
		return api.RPZUpstream{}, err
	}
	return api.RPZUpstream{
		ID: u.ID, Name: u.Name, Upstream: u.Upstream, Zone: u.Zone,
		HasTSIG: u.TSIGKey != "", IntervalS: u.IntervalS, Serial: u.Serial,
		TotalDomains: u.TotalDomains, LastSyncAt: last, LastError: u.LastError,
		SourceID: u.SourceID, ProfileID: u.ProfileID, ProfileName: prof,
		CreatedAt: cre, UpdatedAt: u2,
	}, nil
}

// rpzFrom is the join the CRUD reads share; the alias is 's' for blocklist_sources.
const rpzFrom = ` FROM rpz_upstreams u JOIN blocklist_sources s ON s.id = u.source_id`

func (s *Service) listRPZUpstreams(w http.ResponseWriter, r *http.Request) {
	rows, _ := s.d.Pool.Query(r.Context(), "SELECT "+rpzFullCols+rpzFrom+" ORDER BY u.created_at, u.name")
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (api.RPZUpstream, error) { return scanRPZFull(row) })
	if err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	if items == nil {
		items = []api.RPZUpstream{}
	}
	httpx.WriteJSON(w, http.StatusOK, api.List[api.RPZUpstream]{Items: items, Total: len(items)})
}

func (s *Service) createRPZUpstream(w http.ResponseWriter, r *http.Request) {
	var req api.RPZUpstreamCreate
	if err := httpx.ReadJSON(r, &req, httpx.MaxBody); err != nil {
		httpx.BadRequest(w, err.Error())
		return
	}
	req.Name, req.Upstream, req.Zone = strings.TrimSpace(req.Name), strings.TrimSpace(req.Upstream), strings.TrimSpace(req.Zone)
	if msg := validateRPZCreate(&req); msg != "" {
		httpx.BadRequest(w, msg)
		return
	}
	if req.IntervalS == 0 {
		req.IntervalS = 900
	}
	interval, msg := validInterval(req.IntervalS)
	if msg != "" {
		httpx.BadRequest(w, msg)
		return
	}
	req.IntervalS = interval
	zone := dnsZone(req.Zone)
	if err := s.checkProfile(r.Context(), req.ProfileID); err != nil {
		httpx.BadRequest(w, err.Error())
		return
	}
	var out api.RPZUpstream
	err := pgx.BeginFunc(r.Context(), s.d.Pool, func(tx pgx.Tx) error {
		var srcID string
		// kind rpz_domains carries no url; the transferred content is filled by the slave.
		if err := tx.QueryRow(r.Context(), `INSERT INTO blocklist_sources (name, kind, url, content, enabled, profile_id)
			VALUES ($1, $2, '', '', true, nullif($3, '')::uuid) RETURNING id`,
			req.Name, api.SourceRPZDomains, req.ProfileID).Scan(&srcID); err != nil {
			return err
		}
		if err := tx.QueryRow(r.Context(), `INSERT INTO rpz_upstreams
			(name, upstream, zone, tsig_key, tsig_secret, interval_s, source_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			req.Name, req.Upstream, zone, req.TSIGKey, req.TSIGSecret, req.IntervalS, srcID).Scan(&out.ID); err != nil {
			return err
		}
		return audit.Record(r, tx, "blocklist.rpz.create", "rpz_upstream", out.ID,
			map[string]any{"name": req.Name, "upstream": req.Upstream, "zone": zone, "profile_id": req.ProfileID})
	})
	if db.IsForeignKeyViolation(err) || db.IsInvalidInput(err) {
		httpx.BadRequest(w, "profile_id: no such profile")
		return
	} else if err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	if u, err := scanRPZFull(s.d.Pool.QueryRow(r.Context(), "SELECT "+rpzFullCols+rpzFrom+" WHERE u.id = $1", out.ID)); err != nil {
		httpx.WriteDBError(w, r, err)
		return
	} else {
		out = u
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (s *Service) patchRPZUpstream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req api.RPZUpstreamPatch
	if err := httpx.ReadJSON(r, &req, httpx.MaxBody); err != nil {
		httpx.BadRequest(w, err.Error())
		return
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		httpx.BadRequest(w, "name must not be empty")
		return
	}
	if req.Zone != nil {
		z := dnsZone(strings.TrimSpace(*req.Zone))
		if z == "." {
			httpx.BadRequest(w, "zone must not be empty")
			return
		}
		req.Zone = &z
	}
	// interval_s: null leaves it; a value must be within range (0 is a client error, not
	// "keep", so the dialog's explicit submit cannot silently reset it to the default).
	if req.IntervalS != nil {
		interval, msg := validInterval(*req.IntervalS)
		if msg != "" {
			httpx.BadRequest(w, msg)
			return
		}
		req.IntervalS = &interval
	}
	// TSIG is a pair operation: to set or change it, send both tsig_key and tsig_secret; to
	// clear it, send tsig_key:"" alone (an empty secret alongside is ignored). Any lone
	// non-empty field, or an empty key paired with a non-empty secret, is a 400: either
	// would leave the upstream signing with a mismatched key/secret and fail every transfer.
	// tsig_secret:null alone is ignored (write-only field).
	keySet, secretSet := req.TSIGKey != nil, req.TSIGSecret != nil
	switch {
	case keySet && strings.TrimSpace(*req.TSIGKey) == "" && secretSet && strings.TrimSpace(*req.TSIGSecret) != "":
		// Empty key with a non-empty secret is a client error: reject it rather than
		// silently clearing TSIG (a typo'd key must not disable signing).
		httpx.BadRequest(w, "tsig_key must not be empty when tsig_secret is set (or send tsig_key:\"\" alone to clear)")
		return
	case keySet && strings.TrimSpace(*req.TSIGKey) == "":
		// Clearing TSIG: store "" for both, ignoring any empty secret sent alongside.
		empty := ""
		req.TSIGKey, req.TSIGSecret = &empty, &empty
	case keySet && !secretSet:
		httpx.BadRequest(w, "tsig_secret is required when setting tsig_key (or send tsig_key:\"\" to clear)")
		return
	case secretSet && !keySet:
		httpx.BadRequest(w, "tsig_key is required with tsig_secret")
		return
	case keySet && secretSet && strings.TrimSpace(*req.TSIGSecret) == "":
		httpx.BadRequest(w, "tsig_secret must not be empty when setting tsig_key (or send tsig_key:\"\" to clear)")
		return
	}
	var out api.RPZUpstream
	err := pgx.BeginFunc(r.Context(), s.d.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `UPDATE rpz_upstreams SET
			name = coalesce($2, name),
			upstream = coalesce($3, upstream),
			zone = coalesce($4, zone),
			tsig_key = coalesce($5, tsig_key),
			tsig_secret = coalesce($6, tsig_secret),
			interval_s = coalesce($7, interval_s),
			serial = CASE WHEN $3::text IS NOT NULL OR $4::text IS NOT NULL OR $5::text IS NOT NULL THEN 0 ELSE serial END,
			updated_at = now()
			WHERE id = $1 RETURNING id`, id, req.Name, req.Upstream, req.Zone,
			req.TSIGKey, req.TSIGSecret, req.IntervalS).Scan(&out.ID); err != nil {
			return err
		}
		// An upstream change forces a full transfer on the next sync.
		if err := syncUpstreamName(tx, r, out.ID); err != nil {
			return err
		}
		return audit.Record(r, tx, "blocklist.rpz.update", "rpz_upstream", id, req)
	})
	if db.IsNotFound(err) {
		httpx.NotFound(w)
		return
	} else if err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	if u, err := scanRPZFull(s.d.Pool.QueryRow(r.Context(), "SELECT "+rpzFullCols+rpzFrom+" WHERE u.id = $1", out.ID)); err != nil {
		httpx.WriteDBError(w, r, err)
		return
	} else {
		out = u
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// syncUpstreamName keeps the paired source's name in step with the upstream's, so the
// blocklist UI shows the same label. A no-op when the name did not change.
func syncUpstreamName(tx pgx.Tx, r *http.Request, id string) error {
	_, err := tx.Exec(r.Context(), `UPDATE blocklist_sources s SET name = u.name, updated_at = now()
		FROM rpz_upstreams u WHERE u.id = $1 AND s.id = u.source_id AND s.name <> u.name`, id)
	return err
}

func (s *Service) deleteRPZUpstream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var name, srcID string
	err := pgx.BeginFunc(r.Context(), s.d.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(),
			"DELETE FROM rpz_upstreams WHERE id = $1 RETURNING name, source_id", id).Scan(&name, &srcID); err != nil {
			return err
		}
		// The FK cascades source->upstream, not the other way, so the paired source is
		// removed here explicitly.
		if _, err := tx.Exec(r.Context(), "DELETE FROM blocklist_sources WHERE id = $1", srcID); err != nil {
			return err
		}
		return audit.Record(r, tx, "blocklist.rpz.delete", "rpz_upstream", id, map[string]string{"name": name})
	})
	if db.IsNotFound(err) {
		httpx.NotFound(w)
		return
	} else if err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	// The source row is gone; drop its cached file too.
	os.Remove(s.sourcePath(srcID))
	w.WriteHeader(http.StatusNoContent)
}

// syncRPZUpstream forces one immediate AXFR/IXFR regardless of the interval.
func (s *Service) syncRPZUpstream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	up, err := scanRPZUpstream(s.d.Pool.QueryRow(r.Context(),
		"SELECT "+rpzCols+rpzFrom+" WHERE u.id = $1", id))
	if db.IsNotFound(err) {
		httpx.NotFound(w)
		return
	} else if err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	if err := s.rpzSyncUpstream(r.Context(), up, true); err != nil {
		// The failure is recorded on the upstream (last_error); report it to the caller too
		// so a manual sync surfaces transfer problems instead of a silent 202.
		httpx.WriteError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	_ = audit.Record(r, s.d.Pool, "blocklist.rpz.sync", "rpz_upstream", id, map[string]any{"manual": true})
	out, err := scanRPZFull(s.d.Pool.QueryRow(r.Context(), "SELECT "+rpzFullCols+rpzFrom+" WHERE u.id = $1", id))
	if err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// validateRPZCreate returns a 400 message for a bad create payload, or "".
func validateRPZCreate(req *api.RPZUpstreamCreate) string {
	switch {
	case req.Name == "":
		return "name is required"
	case req.Upstream == "":
		return "upstream is required (host or host:port)"
	case req.Zone == "":
		return "zone is required"
	case req.TSIGSecret != "" && req.TSIGKey == "":
		return "tsig_key is required with tsig_secret"
	case req.TSIGKey != "" && req.TSIGSecret == "":
		return "tsig_secret is required with tsig_key"
	}
	return ""
}

// validInterval clamps/validates interval_s to the schema's 60..86400.
func validInterval(v int) (int, string) {
	if v < 60 || v > 86400 {
		return 0, "interval_s must be between 60 and 86400"
	}
	return v, ""
}
