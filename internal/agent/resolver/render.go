// Package resolver renders the PowerDNS Recursor / Unbound configuration of a recursor
// host (SPEC §20) and parses the resolvers' statistics. It is pure: the agent writes the
// files and reloads the service.
package resolver

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/billyriantono/dnsjos/internal/shared/api"
)

// Files the agent owns. The main file changes need a restart; the ACL file a reload.
const (
	PDNSMain     = "/etc/powerdns/recursor.d/dnsjos.yml"
	PDNSACL      = "/etc/powerdns/dnsjos-allow.txt"
	UnboundMain  = "/etc/unbound/unbound.conf.d/dnsjos.conf"
	UnboundACL   = "/etc/unbound/unbound.conf.d/dnsjos-acl.conf"
	UnboundCtl   = "/etc/unbound/unbound.conf.d/remote-control.conf" // Debian ships it
	Port         = 53
	ProbeAddress = "127.0.0.1:53"
)

// Facts are what the agent knows about the host.
type Facts struct {
	ListenIPs []string // local addresses to answer on (127.0.0.1 is always added)
	Threads   int
	MemMB     int64
	IPv6      bool // the host reaches IPv6 name servers
	// UnboundControl: Debian's remote-control.conf already enables unbound-control.
	UnboundControl bool
}

type File struct {
	Path    string
	Content string
}

// ServiceName is the systemd unit of a resolver kind.
func ServiceName(kind string) string {
	if kind == api.RecursorUnbound {
		return "unbound"
	}
	return "pdns-recursor"
}

// Render returns the main config and the ACL file for c on a host with facts f. It
// fails (and the agent keeps the running config) on settings that are not valid.
func Render(c api.RecursorConfig, f Facts) (main, acl File, err error) {
	s := c.Settings
	s.AllowExtra = nil // already merged into c.Allow; SafeAllow vets every entry again
	if err := s.Validate(); err != nil {
		return main, acl, err
	}
	listen, err := listenAddrs(f.ListenIPs)
	if err != nil {
		return main, acl, err
	}
	allow := SafeAllow(c.Allow)
	threads := max(f.Threads, 1)
	switch c.Kind {
	case api.RecursorPDNS:
		return File{PDNSMain, renderPDNS(c, f, listen, threads)},
			File{PDNSACL, "# Managed by dnsjos-agent: every DnsJos node plus the panel's extra entries.\n" + strings.Join(allow, "\n") + "\n"}, nil
	case api.RecursorUnbound:
		var b strings.Builder
		b.WriteString("# Managed by dnsjos-agent: every DnsJos node plus the panel's extra entries.\nserver:\n")
		for _, a := range allow {
			fmt.Fprintf(&b, "    access-control: %s allow\n", a)
		}
		return File{UnboundMain, renderUnbound(c, f, listen, threads)}, File{UnboundACL, b.String()}, nil
	}
	return main, acl, fmt.Errorf("unknown resolver kind %q", c.Kind)
}

// listenAddrs validates the local addresses and adds the loopback the agent probes.
func listenAddrs(ips []string) ([]netip.Addr, error) {
	out := []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	for _, s := range ips {
		a, err := netip.ParseAddr(s)
		if err != nil || a.Zone() != "" || a.IsUnspecified() {
			return nil, fmt.Errorf("listen address %q is not a usable IP address", s)
		}
		if a = a.Unmap(); !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out, nil
}

// SafeAllow is the allow-list as written: loopback always, entries that are not
// addresses/CIDRs or are too wide (api.CheckAllowEntry) dropped, sorted and deduplicated.
func SafeAllow(in []string) []string {
	set := map[string]bool{"127.0.0.0/8": true, "::1/128": true}
	for _, s := range in {
		if api.CheckAllowEntry(s) != nil {
			continue
		}
		p, _ := api.AllowPrefix(s)
		set[p.String()] = true
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// publicIPv4 reports whether s is an IPv4 address that may be sent as ECS: never a
// private, CGNAT, loopback or link-local one (the ranges dnsdist strips).
func publicIPv4(s string) bool {
	a, err := netip.ParseAddr(s)
	if err != nil || !a.Is4() {
		return false
	}
	return a.IsGlobalUnicast() && !a.IsPrivate() && !netip.MustParsePrefix("100.64.0.0/10").Contains(a)
}

// pdnsEntries is the record cache size: the installer's ~250 entries per MB of RAM
// (200k..2M), or ~1 KB per entry of an explicit cache size.
func pdnsEntries(s api.RecursorSettings, memMB int64) int64 {
	if s.CacheMB > 0 {
		return int64(s.CacheMB) * 1000
	}
	return min(max(memMB*250, 200_000), 2_000_000)
}

// unboundRRsetMB: the installer's RAM/16 (64..1024 MB), or 2/5 of an explicit size (the
// msg cache takes half of that again and Unbound uses ~1.5x what is configured).
func unboundRRsetMB(s api.RecursorSettings, memMB int64) int64 {
	if s.CacheMB > 0 {
		return max(int64(s.CacheMB)*2/5, 16)
	}
	return min(max(memMB/16, 64), 1024)
}

func renderPDNS(c api.RecursorConfig, f Facts, listen []netip.Addr, threads int) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w("# Managed by dnsjos-agent (DnsJos recursor): change the settings in the panel, not here.")
	w("incoming:")
	w("  listen:")
	for _, a := range listen {
		w("    - '%s'", netip.AddrPortFrom(a, Port))
	}
	w("  # Listen addresses may come up after the service (boot, DHCP).")
	w("  non_local_bind: true")
	w("  allow_from_file: %s", PDNSACL)
	if c.Settings.ECS {
		w("  use_incoming_edns_subnet: true")
	}
	w("recursor:")
	w("  threads: %d", threads)
	w("outgoing:")
	w("  source_address:")
	w("    - 0.0.0.0")
	if f.IPv6 {
		w("    - '::'")
	}
	if c.Settings.ECS {
		w("  edns_subnet_allow_list:")
		zones := c.Settings.ECSZones
		if len(zones) == 0 {
			zones = []string{"0.0.0.0/0", "::/0"}
		}
		for _, z := range zones {
			w("    - '%s'", z)
		}
	}
	w("dnssec:")
	w("  # Always validate (the default \"process\" only validates when the client asks).")
	w("  validation: validate")
	n := pdnsEntries(c.Settings, f.MemMB)
	w("recordcache:")
	w("  max_entries: %d", n)
	w("  # Refresh names when 10%% of their TTL is left; serve stale for up to a day (RFC 8767).")
	w("  refresh_on_ttl_perc: 10")
	w("  serve_stale_extensions: 2880")
	w("packetcache:")
	w("  max_entries: %d", n/2)
	if c.Settings.ECS && publicIPv4(c.PublicIP) {
		w("ecs:")
		w("  # Queries without a client subnet: answer for this host's location, never a private one.")
		w("  scope_zero_address: %s", c.PublicIP)
	}
	return b.String()
}

func renderUnbound(c api.RecursorConfig, f Facts, listen []netip.Addr, threads int) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	slabs := 1
	for slabs < threads {
		slabs *= 2
	}
	rr := unboundRRsetMB(c.Settings, f.MemMB)
	yn := map[bool]string{true: "yes", false: "no"}
	w("# Managed by dnsjos-agent (DnsJos recursor): change the settings in the panel, not here.")
	w("server:")
	for _, a := range listen {
		w("    interface: %s@%d", a, Port)
	}
	w("    # Listen addresses may come up after the service (boot, DHCP).")
	w("    ip-freebind: yes")
	w("    do-ip6: %s", yn[f.IPv6])
	w("    hide-identity: yes")
	w("    hide-version: yes")
	w("    extended-statistics: yes")
	w("    num-threads: %d", threads)
	w("    so-reuseport: yes")
	w("    so-rcvbuf: 4m")
	w("    so-sndbuf: 4m")
	w("    outgoing-range: 8192")
	w("    num-queries-per-thread: 4096")
	w("    incoming-num-tcp: 256")
	w("    outgoing-num-tcp: 64")
	for _, k := range []string{"msg", "rrset", "infra", "key"} {
		w("    %s-cache-slabs: %d", k, slabs)
	}
	w("    rrset-cache-size: %dm", rr)
	w("    msg-cache-size: %dm", max(rr/2, 1))
	w("    key-cache-size: %dm", max(rr/8, 1))
	w("    neg-cache-size: %dm", max(rr/16, 1))
	w("    cache-max-negative-ttl: 300")
	w("    prefetch: yes")
	w("    prefetch-key: yes")
	w("    serve-expired: yes")
	w("    serve-expired-ttl: 86400")
	w("    serve-expired-client-timeout: 0")
	w("    aggressive-nsec: yes")
	w("    qname-minimisation: yes")
	w("    rrset-roundrobin: yes")
	w("    harden-below-nxdomain: yes")
	if c.Settings.ECS {
		w("    # EDNS Client Subnet from dnsdist, passed on to authoritative servers")
		w("    module-config: \"subnetcache validator iterator\"")
		if len(c.Settings.ECSZones) == 0 {
			w("    send-client-subnet: 0.0.0.0/0")
			w("    send-client-subnet: ::0/0")
		}
		for _, z := range c.Settings.ECSZones {
			w("    client-subnet-zone: %s", z)
		}
	}
	if !f.UnboundControl {
		w("remote-control:")
		w("    control-enable: yes")
		w("    control-interface: /run/unbound.ctl")
	}
	return b.String()
}
