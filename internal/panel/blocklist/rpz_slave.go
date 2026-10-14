package blocklist

// RPZ slave (SPEC §7.7): the panel pulls the blocklist zone from an RPZ master over
// AXFR/IXFR and writes the transferred domain list into the linked source's content, so
// the build afterwards treats it exactly like a manual source. Only the panel needs the
// upstream's allow-IP/TSIG; nodes keep downloading the CDB from the panel.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/miekg/dns"

	"github.com/billyriantono/dnsjos/internal/panel/audit"
	"github.com/billyriantono/dnsjos/internal/panel/db"
)

const rpzMaxTransferRecords = 10_000_000

// rpzUpstream mirrors one rpz_upstreams row joined to its source.
type rpzUpstream struct {
	ID, Name, Upstream, Zone string
	TSIGKey                  string
	TSIGSecret               string
	IntervalS                int
	Serial                   uint32
	TotalDomains             int
	LastError                string
	SourceID                 string
	ProfileID                string
}

const rpzCols = `u.id, u.name, u.upstream, u.zone, u.tsig_key, u.tsig_secret,
	u.interval_s, u.serial, u.total_domains, coalesce(u.last_error, ''), u.source_id,
	coalesce(s.profile_id::text, '')`

func scanRPZUpstream(row pgx.Row) (rpzUpstream, error) {
	var u rpzUpstream
	err := row.Scan(&u.ID, &u.Name, &u.Upstream, &u.Zone, &u.TSIGKey, &u.TSIGSecret,
		&u.IntervalS, &u.Serial, &u.TotalDomains, &u.LastError, &u.SourceID, &u.ProfileID)
	return u, err
}

// rpzSyncAll is the scheduled job: sync every upstream whose source is still enabled and
// whose interval has passed. A failing upstream must not stop the others.
func (s *Service) rpzSyncAll(ctx context.Context) error {
	rows, err := s.d.Pool.Query(ctx, `SELECT `+rpzCols+` FROM rpz_upstreams u
		JOIN blocklist_sources s ON s.id = u.source_id
		WHERE s.enabled ORDER BY u.id`)
	if err != nil {
		return err
	}
	ups, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (rpzUpstream, error) { return scanRPZUpstream(row) })
	if err != nil {
		return err
	}
	var errs []error
	for _, up := range ups {
		if err := s.rpzSyncUpstream(ctx, up, false); err != nil {
			errs = append(errs, fmt.Errorf("rpz %s: %w", up.Name, err))
		}
	}
	return errors.Join(errs...)
}

// rpzSyncUpstream serves both the interval job and a manual sync. It queries the
// upstream's SOA, transfers the zone when the upstream is newer, atomically rewrites the
// source's content and bumps the serial. force skips the interval check.
func (s *Service) rpzSyncUpstream(ctx context.Context, up rpzUpstream, force bool) error {
	if !force {
		last, err := lastRPZSync(ctx, s.d.Pool, up.ID)
		if err != nil {
			return err
		}
		if last != nil && time.Since(*last) < time.Duration(up.IntervalS)*time.Second {
			return nil
		}
	}
	serial, domains, err := s.transferRPZ(ctx, up)
	if err != nil {
		s.recordRPZError(ctx, up.ID, err)
		return err
	}
	if serial == up.Serial && serial != 0 {
		return nil // upstream not newer
	}
	content := strings.Join(domains, "\n")
	if len(domains) > 0 {
		content += "\n"
	}
	if _, err := s.d.Pool.Exec(ctx, `UPDATE blocklist_sources SET content = $2, updated_at = now() WHERE id = $1`,
		up.SourceID, content); err != nil {
		s.recordRPZError(ctx, up.ID, err)
		return err
	}
	if _, err := s.d.Pool.Exec(ctx, `UPDATE rpz_upstreams SET serial = $2, total_domains = $3,
		last_sync_at = now(), last_error = '' WHERE id = $1`, up.ID, serial, len(domains)); err != nil {
		return err
	}
	_ = audit.Log(ctx, s.d.Pool, nil, "blocklist.rpz.sync", "rpz_upstream", up.ID,
		map[string]any{"serial": serial, "domains": len(domains)}, "")
	return nil
}

func lastRPZSync(ctx context.Context, q db.Querier, id string) (*time.Time, error) {
	var last *time.Time
	err := q.QueryRow(ctx, "SELECT last_sync_at FROM rpz_upstreams WHERE id = $1", id).Scan(&last)
	return last, err
}

func (s *Service) recordRPZError(ctx context.Context, id string, err error) {
	_, _ = s.d.Pool.Exec(ctx, "UPDATE rpz_upstreams SET last_error = $2 WHERE id = $1", id, err.Error())
}

// tsigSecret returns the miekg/dns TSIG map for the upstream, or nil without TSIG.
func (u rpzUpstream) tsigSecret() map[string]string {
	if u.TSIGKey == "" || u.TSIGSecret == "" {
		return nil
	}
	return map[string]string{dns.Fqdn(u.TSIGKey): u.TSIGSecret}
}

func (u rpzUpstream) sign(m *dns.Msg) {
	if u.TSIGKey != "" {
		m.SetTsig(dns.Fqdn(u.TSIGKey), dns.HmacSHA256, 300, time.Now().Unix())
	}
}

// transferRPZ does the whole pull: SOA serial, then AXFR (no local serial) or IXFR.
// It returns the upstream serial and the full domain list (already normalized).
func (s *Service) transferRPZ(ctx context.Context, up rpzUpstream) (uint32, []string, error) {
	zone := dns.Fqdn(up.Zone)
	serial, err := s.rpzSOA(ctx, up, zone)
	if err != nil {
		return 0, nil, err
	}
	req := new(dns.Msg)
	if up.Serial == 0 {
		req.SetAxfr(zone)
	} else {
		req.SetIxfr(zone, up.Serial, "ns1."+zone, "hostmaster."+zone)
	}
	up.sign(req)
	tr := &dns.Transfer{DialTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second,
		WriteTimeout: 5 * time.Second, TsigSecret: up.tsigSecret()}
	envs, err := tr.In(req, up.Upstream)
	if err != nil {
		return 0, nil, fmt.Errorf("transfer: %w", err)
	}
	var records []dns.RR
	for env := range envs {
		if env.Error != nil {
			return 0, nil, fmt.Errorf("transfer stream: %w", env.Error)
		}
		if len(records)+len(env.RR) > rpzMaxTransferRecords {
			return 0, nil, fmt.Errorf("transfer exceeds %d records", rpzMaxTransferRecords)
		}
		records = append(records, env.RR...)
	}
	domains, err := rpzOwners(records, zone)
	if err != nil {
		return 0, nil, err
	}
	return serial, domains, nil
}

func (s *Service) rpzSOA(ctx context.Context, up rpzUpstream, zone string) (uint32, error) {
	m := new(dns.Msg)
	m.SetQuestion(zone, dns.TypeSOA)
	up.sign(m)
	c := &dns.Client{Timeout: 5 * time.Second, TsigSecret: up.tsigSecret()}
	r, _, err := c.ExchangeContext(ctx, m, up.Upstream)
	if err != nil {
		return 0, fmt.Errorf("SOA query: %w", err)
	}
	if r.Truncated || r.Rcode != dns.RcodeSuccess {
		return 0, fmt.Errorf("SOA unusable: truncated=%t rcode=%s", r.Truncated, dns.RcodeToString[r.Rcode])
	}
	for _, a := range r.Answer {
		if soa, ok := a.(*dns.SOA); ok && strings.EqualFold(soa.Hdr.Name, zone) {
			return soa.Serial, nil
		}
	}
	return 0, errors.New("no zone SOA in response")
}

// rpzOwners extracts the blocked domains from a transfer: every owner of a CNAME/A/AAAA
// record inside the zone, minus the zone apex, normalized by the same rules the build
// uses. Duplicates are collapsed.
func rpzOwners(records []dns.RR, zone string) ([]string, error) {
	seen := make(map[string]struct{}, len(records))
	for _, rr := range records {
		owner := strings.ToLower(dns.Fqdn(rr.Header().Name))
		if !dns.IsSubDomain(zone, owner) {
			return nil, fmt.Errorf("owner outside RPZ zone: %s", owner)
		}
		if owner == zone {
			continue
		}
		switch rr.Header().Rrtype {
		case dns.TypeCNAME, dns.TypeA, dns.TypeAAAA:
		default:
			continue
		}
		domain := strings.TrimSuffix(strings.TrimSuffix(owner, zone), ".")
		if name, ok := normalizeDomain(domain); ok {
			seen[name] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out, nil
}
