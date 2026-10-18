package recursors

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"net/netip"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/billyriantono/dnsjos/internal/panel/db"
	"github.com/billyriantono/dnsjos/internal/panel/nodes"
	"github.com/billyriantono/dnsjos/internal/shared/api"
)

// fleetAllow is the automatic part of every recursor's allow-list: the public IPv4 of
// every live node and the public IPv6 it reports (SPEC §20). The whole fleet is allowed,
// not only the nodes that use a recursor: deriving it from profiles would race with
// publishes and staged rollouts (a node querying before the recursor allows it gets
// REFUSED), and every entry is still one of the operator's own servers.
func fleetAllow(ctx context.Context, q db.Querier) ([]string, error) {
	rows, _ := q.Query(ctx, `SELECT public_ip, coalesce(last_heartbeat->>'public_ipv6', '')
		FROM nodes WHERE deleted_at IS NULL`)
	set := map[string]bool{}
	var v4, v6 string
	_, err := pgx.ForEachRow(rows, []any{&v4, &v6}, func() error {
		for _, s := range []string{v4, v6} {
			// Only public addresses; a node behind NAT reaches the recursor from its public one.
			if a, err := netip.ParseAddr(s); err == nil && a.Zone() == "" && a.IsGlobalUnicast() {
				a = a.Unmap()
				set[netip.PrefixFrom(a, a.BitLen()).String()] = true
			}
		}
		return nil
	})
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	slices.Sort(out)
	return out, err
}

// allowList is the complete list sent to the agent: loopback, the fleet and the
// recursor's extra entries, deduplicated and sorted.
func allowList(fleet []string, s api.RecursorSettings) []string {
	set := map[string]bool{"127.0.0.0/8": true, "::1/128": true}
	for _, p := range fleet {
		set[p] = true
	}
	for _, e := range s.AllowExtra {
		if api.CheckAllowEntry(e) == nil {
			p, _ := api.AllowPrefix(e)
			set[p.String()] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// desiredConfig is what the recursor's agent should run; Version changes with any input.
func desiredConfig(kind, publicIP string, s api.RecursorSettings, fleet []string) api.RecursorConfig {
	c := api.RecursorConfig{Kind: kind, PublicIP: publicIP, Allow: allowList(fleet, s), Settings: s}
	b, _ := json.Marshal(c)
	h := fnv.New32a()
	h.Write(b)
	c.Version = int(h.Sum32()%2147483646) + 1
	return c
}

// usedBy lists the node upstreams whose address is the recursor's public or listen IP.
// Upstreams are literal ip:port in the spec (validated), so no name resolution is needed.
func usedBy(ups []nodes.NodeUpstream, ips ...string) []api.RecursorUser {
	mine := map[netip.Addr]bool{}
	for _, s := range ips {
		if a, err := netip.ParseAddr(s); err == nil {
			mine[a.Unmap()] = true
		}
	}
	out := []api.RecursorUser{}
	for _, n := range ups {
		for _, u := range n.Servers {
			if ap, err := netip.ParseAddrPort(u.Address); err == nil && mine[ap.Addr().Unmap()] {
				out = append(out, api.RecursorUser{NodeID: n.NodeID, NodeName: n.NodeName, Address: u.Address, ECS: u.ECS})
			}
		}
	}
	return out
}
