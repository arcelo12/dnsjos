package recursors

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/billyriantono/dnsjos/internal/panel/app"
	"github.com/billyriantono/dnsjos/internal/panel/audit"
	"github.com/billyriantono/dnsjos/internal/panel/db"
	"github.com/billyriantono/dnsjos/internal/panel/httpx"
	"github.com/billyriantono/dnsjos/internal/shared/api"
)

// Same rule as node names, so a recursor name is a valid upstream name in a profile.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func validName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("name %q: 1-64 letters, digits, '.', '_' or '-', starting with a letter or digit", name)
	}
	return nil
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// nameFromHostname turns a hostname into a valid recursor name.
func nameFromHostname(h string) string {
	b := []byte(clip(h, 64))
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			b[i] = '-'
		}
	}
	return strings.TrimLeft(string(b), "._-")
}

// defaultSettings: ECS on, the reason most recursors are added (CDN answers for the
// customer's location); everything else automatic.
var defaultSettings = api.RecursorSettings{AllowExtra: []string{}, ECS: true, ECSZones: []string{}}

func (s *svc) listTokens(w http.ResponseWriter, r *http.Request) {
	rows, _ := s.d.Pool.Query(r.Context(), `SELECT id, name, kind, created_by, created_at, expires_at, used_at, used_by
		FROM recursor_tokens ORDER BY created_at DESC LIMIT 500`)
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (api.RecursorToken, error) {
		var t api.RecursorToken
		err := row.Scan(&t.ID, &t.Name, &t.Kind, &t.CreatedBy, &t.CreatedAt, &t.ExpiresAt, &t.UsedAt, &t.UsedBy)
		return t, err
	})
	if err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	if items == nil {
		items = []api.RecursorToken{}
	}
	httpx.WriteJSON(w, http.StatusOK, api.List[api.RecursorToken]{Items: items, Total: len(items)})
}

func (s *svc) createToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req api.RecursorTokenCreate
	if err := httpx.ReadJSON(r, &req, httpx.MaxJSON); err != nil {
		httpx.BadRequest(w, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name != "" {
		if err := validName(req.Name); err != nil {
			httpx.BadRequest(w, err.Error())
			return
		}
	}
	if !slices.Contains(api.RecursorKinds, req.Kind) {
		httpx.BadRequest(w, "kind: must be one of "+strings.Join(api.RecursorKinds, ", "))
		return
	}
	if req.TTLHours == 0 {
		req.TTLHours = 24
	}
	if req.TTLHours < 1 || req.TTLHours > 720 {
		httpx.BadRequest(w, "ttl_hours must be 1..720")
		return
	}
	tok := app.NewToken()
	out := api.RecursorTokenCreated{
		Token: tok,
		InstallCommand: "curl -fsSL " + strings.TrimRight(s.d.Settings.PublicURL(), "/") +
			"/install-recursor.sh | sudo sh -s -- --token " + tok + " --kind " + req.Kind,
		ExpiresAt: time.Now().Add(time.Duration(req.TTLHours) * time.Hour).UTC().Truncate(time.Second),
	}
	err := pgx.BeginFunc(ctx, s.d.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO recursor_tokens (token_hash, name, kind, created_by, expires_at)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`, app.HashToken(tok), req.Name, req.Kind,
			app.UserIDFrom(ctx), out.ExpiresAt).Scan(&out.ID); err != nil {
			return err
		}
		return audit.Record(r, tx, "recursor_token.create", "recursor_token", out.ID, req)
	})
	if err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (s *svc) deleteToken(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), r.PathValue("id")
	err := pgx.BeginFunc(ctx, s.d.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "DELETE FROM recursor_tokens WHERE id = $1 RETURNING id", id).Scan(&id); err != nil {
			return err
		}
		return audit.Record(r, tx, "recursor_token.delete", "recursor_token", id, nil)
	})
	if err != nil {
		httpx.WriteDBError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var (
	errUnusableName = errors.New("hostname is not usable as a recursor name; set a name on the token")
	// An unnamed token never re-keys an existing recursor: a leaked token could take
	// over a live recursor by claiming its hostname.
	errNameTaken    = errors.New("a recursor with this name already exists; to re-enroll it, create a token with its name")
	errKindMismatch = errors.New("the token is for another resolver kind than the installer set up (--kind)")
)

// enroll claims a single-use token and creates the recursor, or re-keys the live recursor
// of the same name (re-installing keeps its settings and history) when the token names it.
func (s *svc) enroll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req api.RecursorEnrollRequest
	if err := httpx.ReadJSON(r, &req, 64<<10); err != nil {
		httpx.BadRequest(w, err.Error())
		return
	}
	if req.Token == "" {
		httpx.BadRequest(w, "token required")
		return
	}
	ip := clip(req.PublicIP, 64)
	if _, err := netip.ParseAddr(ip); err != nil {
		ip = httpx.ClientIP(r)
	}
	recTok := app.NewToken()
	var id, name, kind string
	err := pgx.BeginFunc(ctx, s.d.Pool, func(tx pgx.Tx) error {
		var tokID string
		if err := tx.QueryRow(ctx, `UPDATE recursor_tokens SET used_at = now()
			WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
			RETURNING id, name, kind`, app.HashToken(req.Token)).Scan(&tokID, &name, &kind); err != nil {
			return err
		}
		if req.Kind != "" && req.Kind != kind {
			return errKindMismatch
		}
		scoped := name != ""
		if !scoped {
			name = nameFromHostname(req.Hostname)
		}
		if validName(name) != nil {
			return errUnusableName
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO recursors (name, kind, hostname, public_ip, settings, token_hash, enrolled_at,
			                       agent_version, resolver_version, os, arch)
			VALUES ($1, $2, $3, $4, $5, $6, now(), $7, $8, $9, $10)
			ON CONFLICT (name) WHERE deleted_at IS NULL DO UPDATE SET
				kind = EXCLUDED.kind, hostname = EXCLUDED.hostname, public_ip = EXCLUDED.public_ip,
				token_hash = EXCLUDED.token_hash, enrolled_at = now(), last_heartbeat = NULL,
				agent_version = EXCLUDED.agent_version, resolver_version = EXCLUDED.resolver_version,
				os = EXCLUDED.os, arch = EXCLUDED.arch, applied_config_version = NULL
			WHERE $11::bool
			RETURNING id`,
			name, kind, clip(req.Hostname, 253), ip, defaultSettings, app.HashToken(recTok),
			clip(req.AgentVersion, 64), clip(req.ResolverVersion, 128), clip(req.OS, 128), clip(req.Arch, 32), scoped).
			Scan(&id); db.IsNotFound(err) {
			return errNameTaken
		} else if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE recursor_tokens SET used_by = $2 WHERE id = $1", tokID, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, nil, "recursor.enroll", "recursor", id,
			map[string]any{"name": name, "kind": kind, "hostname": req.Hostname, "token_id": tokID}, httpx.ClientIP(r))
	})
	switch {
	case db.IsNotFound(err):
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_token", "recursor token is invalid, expired or already used")
		return
	case errors.Is(err, errUnusableName), errors.Is(err, errKindMismatch):
		httpx.BadRequest(w, err.Error())
		return
	case errors.Is(err, errNameTaken):
		httpx.WriteError(w, http.StatusConflict, "name_taken", err.Error())
		return
	case err != nil:
		httpx.WriteDBError(w, r, err)
		return
	}
	s.forget(id)
	httpx.WriteJSON(w, http.StatusOK, api.RecursorEnrollResponse{RecursorID: id, Name: name, Kind: kind, Token: recTok,
		PollIntervalS: s.d.Settings.Get().AgentPollIntervalS})
}

func (s *svc) forget(id string) {
	s.mu.Lock()
	delete(s.live, id)
	s.mu.Unlock()
}
