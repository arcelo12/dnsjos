package recursors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/billyriantono/dnsjos/internal/panel/app"
	"github.com/billyriantono/dnsjos/internal/panel/audit"
	"github.com/billyriantono/dnsjos/internal/panel/httpx"
	"github.com/billyriantono/dnsjos/internal/panel/nodes"
	"github.com/billyriantono/dnsjos/internal/shared/api"
)

const recursorSelect = `
SELECT id, name, kind, hostname, public_ip, status, settings, enrolled_at, last_seen_at, agent_version,
       resolver_version, os, arch, applied_config_version, last_error, created_at,
       coalesce(last_heartbeat->>'listen_ip', ''), coalesce(last_heartbeat->>'listen_ipv6', '')
FROM recursors WHERE deleted_at IS NULL`

// normSettings maps missing lists to empty ones, so equal settings hash equally.
func normSettings(s api.RecursorSettings) api.RecursorSettings {
	if s.AllowExtra == nil {
		s.AllowExtra = []string{}
	}
	if s.ECSZones == nil {
		s.ECSZones = []string{}
	}
	return s
}

func (s *svc) query(ctx context.Context, where string, args ...any) ([]api.Recursor, error) {
	fleet, err := fleetAllow(ctx, s.d.Pool)
	if err != nil {
		return nil, err
	}
	ups, err := nodes.ServedUpstreams(ctx, s.d.Pool)
	if err != nil {
		return nil, err
	}
	rows, _ := s.d.Pool.Query(ctx, recursorSelect+where+" ORDER BY name", args...)
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (api.Recursor, error) {
		var x api.Recursor
		err := row.Scan(&x.ID, &x.Name, &x.Kind, &x.Hostname, &x.PublicIP, &x.Status, &x.Settings, &x.EnrolledAt,
			&x.LastSeenAt, &x.AgentVersion, &x.ResolverVersion, &x.OS, &x.Arch, &x.AppliedConfigVersion, &x.LastError,
			&x.CreatedAt, &x.ListenIP, &x.ListenIPv6)
		x.Settings = normSettings(x.Settings)
		c := desiredConfig(x.Kind, x.PublicIP, x.Settings, fleet)
		x.Allow, x.DesiredConfigVersion = c.Allow, c.Version
		x.ConfigInSync = x.AppliedConfigVersion != nil && *x.AppliedConfigVersion == c.Version
		x.AgentOutdated = x.AgentVersion != "" && s.d.AgentVersion != "" && x.AgentVersion != s.d.AgentVersion
		x.UsedBy = usedBy(ups, x.PublicIP, x.ListenIP, x.ListenIPv6)
		if x.Status != api.NodeOffline {
			s.mu.Lock()
			e := s.live[x.ID]
			s.mu.Unlock()
			x.QPS, x.CacheHitRatio, x.LatencyAvgMs = e.qps, e.hit, e.hb.LatencyAvgMs
		}
		return x, err
	})
}

func (s *svc) one(ctx context.Context, id string) (api.Recursor, error) {
	items, err := s.query(ctx, " AND id = $1", id)
	if err == nil && len(items) == 0 {
		err = pgx.ErrNoRows
	}
	if err != nil {
		return api.Recursor{}, err
	}
	return items[0], nil
}

func (s *svc) list(w http.ResponseWriter, r *http.Request) {
	items, err := s.query(r.Context(), "")
	if err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	if items == nil {
		items = []api.Recursor{}
	}
	httpx.WriteJSON(w, http.StatusOK, api.List[api.Recursor]{Items: items, Total: len(items)})
}

func (s *svc) get(w http.ResponseWriter, r *http.Request) {
	x, err := s.one(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, x)
}

func (s *svc) patch(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), r.PathValue("id")
	var p api.RecursorPatch
	if err := httpx.ReadJSON(r, &p, httpx.MaxJSON); err != nil {
		httpx.BadRequest(w, err.Error())
		return
	}
	cur, err := s.one(ctx, id)
	if err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	if p.Name != nil {
		cur.Name = strings.TrimSpace(*p.Name)
		if err := validName(cur.Name); err != nil {
			httpx.BadRequest(w, err.Error())
			return
		}
	}
	if p.PublicIP != nil {
		cur.PublicIP = strings.TrimSpace(*p.PublicIP)
		if a, err := netip.ParseAddr(cur.PublicIP); err != nil || a.Zone() != "" {
			httpx.BadRequest(w, "public_ip: not an IP address")
			return
		}
	}
	if p.Settings != nil {
		st := normSettings(*p.Settings)
		for i, e := range st.AllowExtra {
			st.AllowExtra[i] = strings.TrimSpace(e)
		}
		for i, z := range st.ECSZones {
			st.ECSZones[i] = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(z), "."))
		}
		st.AllowExtra, st.ECSZones = uniq(st.AllowExtra), uniq(st.ECSZones)
		if err := st.Validate(); err != nil {
			httpx.BadRequest(w, err.Error())
			return
		}
		cur.Settings = st
	}
	err = pgx.BeginFunc(ctx, s.d.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE recursors SET name = $2, public_ip = $3, settings = $4
			WHERE id = $1 AND deleted_at IS NULL`, id, cur.Name, cur.PublicIP, cur.Settings); err != nil {
			return err
		}
		return audit.Record(r, tx, "recursor.update", "recursor", id, p)
	})
	if err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	s.get(w, r)
}

// uniq drops empty and repeated entries, keeping the first occurrence's order.
func uniq(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func (s *svc) remove(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), r.PathValue("id")
	err := pgx.BeginFunc(ctx, s.d.Pool, func(tx pgx.Tx) error {
		var name string
		// Soft delete keeps the metrics; dropping the token hash locks the agent out.
		if err := tx.QueryRow(ctx, `UPDATE recursors SET deleted_at = now(), token_hash = NULL
			WHERE id = $1 AND deleted_at IS NULL RETURNING name`, id).Scan(&name); err != nil {
			return err
		}
		return audit.Record(r, tx, "recursor.delete", "recursor", id, map[string]string{"name": name})
	})
	if err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	s.forget(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *svc) liveGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var raw []byte
	var seen *time.Time
	if err := s.d.Pool.QueryRow(r.Context(), `SELECT last_heartbeat, last_seen_at FROM recursors
		WHERE id = $1 AND deleted_at IS NULL`, id).Scan(&raw, &seen); err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	out := api.RecursorLive{RecursorID: id}
	s.mu.Lock()
	e, ok := s.live[id]
	s.mu.Unlock()
	if ok {
		out.Heartbeat, out.ReceivedAt = &e.hb, &e.at
	} else if raw != nil { // panel restarted since the last heartbeat
		var hb api.RecursorHeartbeat
		if json.Unmarshal(raw, &hb) == nil {
			out.Heartbeat, out.ReceivedAt = &hb, seen
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// command queues a recursor command. ?force=true allows upgrade_agent on an agent that
// already matches the embedded one (reinstall).
func (s *svc) command(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), r.PathValue("id")
	var req api.CommandRequest
	if err := httpx.ReadJSON(r, &req, httpx.MaxJSON); err != nil {
		httpx.BadRequest(w, err.Error())
		return
	}
	var agentVer string
	if err := s.d.Pool.QueryRow(ctx, "SELECT agent_version FROM recursors WHERE id = $1 AND deleted_at IS NULL", id).
		Scan(&agentVer); err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	var err error
	switch {
	case !slices.Contains(api.RecursorCommandTypes, req.Type):
		err = fmt.Errorf("type: must be one of %s", strings.Join(api.RecursorCommandTypes, ", "))
	case req.Type == api.CmdUpgradeAgent && s.d.AgentVersion == "":
		err = errors.New("type: no agent binary is embedded in this panel build")
	case req.Type == api.CmdUpgradeAgent && agentVer == s.d.AgentVersion && r.URL.Query().Get("force") != "true":
		err = fmt.Errorf("type: the agent already runs %q (the embedded version); add ?force=true to reinstall", s.d.AgentVersion)
	}
	if err != nil {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "invalid_command", err.Error())
		return
	}
	var c api.Command
	err = pgx.BeginFunc(ctx, s.d.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO recursor_commands (recursor_id, type, created_by) VALUES ($1, $2, $3)
			RETURNING id, type`, id, req.Type, app.UserIDFrom(ctx)).Scan(&c.ID, &c.Type); err != nil {
			return err
		}
		return audit.Record(r, tx, "recursor.command", "recursor", id, c)
	})
	if err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, c)
}
