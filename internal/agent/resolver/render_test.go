package resolver

import (
	"slices"
	"strings"
	"testing"

	"github.com/billyriantono/dnsjos/internal/shared/api"
)

func cfg(kind string, s api.RecursorSettings) api.RecursorConfig {
	return api.RecursorConfig{Version: 7, Kind: kind, PublicIP: "192.0.2.53",
		Allow: []string{"198.51.100.99/32", "2001:db8::5/128", "127.0.0.0/8"}, Settings: s}
}

var natFacts = Facts{ListenIPs: []string{"10.0.0.43"}, Threads: 2, MemMB: 4096}

func has(t *testing.T, name, text string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("%s: missing %q in:\n%s", name, w, text)
		}
	}
}

func hasNot(t *testing.T, name, text string, bad ...string) {
	t.Helper()
	for _, b := range bad {
		if strings.Contains(text, b) {
			t.Errorf("%s: unexpected %q in:\n%s", name, b, text)
		}
	}
}

func TestRenderPDNS(t *testing.T) {
	main, acl, err := Render(cfg(api.RecursorPDNS, api.RecursorSettings{ECS: true, ECSZones: []string{"googlevideo.com"}}), natFacts)
	if err != nil {
		t.Fatal(err)
	}
	if main.Path != PDNSMain || acl.Path != PDNSACL {
		t.Fatalf("paths %s %s", main.Path, acl.Path)
	}
	// Behind NAT: listen on the local address, ECS scope-zero is the public one.
	has(t, "pdns", main.Content, "- '10.0.0.43:53'", "- '127.0.0.1:53'", "allow_from_file: "+PDNSACL,
		"use_incoming_edns_subnet: true", "edns_subnet_allow_list:\n    - 'googlevideo.com'",
		"scope_zero_address: 192.0.2.53", "validation: validate", "threads: 2",
		"max_entries: 1024000", "max_entries: 512000", "non_local_bind: true")
	hasNot(t, "pdns", main.Content, "0.0.0.0:53", "'::'", "0.0.0.0/0")
	has(t, "pdns acl", acl.Content, "198.51.100.99/32\n", "2001:db8::5/128\n", "127.0.0.0/8\n", "::1/128\n")

	main, _, _ = Render(cfg(api.RecursorPDNS, api.RecursorSettings{ECS: true}), Facts{ListenIPs: []string{"10.0.0.43"}, IPv6: true})
	has(t, "pdns ecs all", main.Content, "- '0.0.0.0/0'\n    - '::/0'", "- '::'", "threads: 1", "max_entries: 200000")

	main, _, _ = Render(cfg(api.RecursorPDNS, api.RecursorSettings{CacheMB: 512}), natFacts)
	has(t, "pdns cache", main.Content, "max_entries: 512000", "max_entries: 256000")
	hasNot(t, "pdns no ecs", main.Content, "edns_subnet", "use_incoming_edns_subnet", "scope_zero_address")

	// A private "public" address must never be sent as ECS.
	c := cfg(api.RecursorPDNS, api.RecursorSettings{ECS: true})
	for _, ip := range []string{"10.1.2.3", "100.64.0.9", "192.168.1.1", "", "2001:db8::1"} {
		c.PublicIP = ip
		main, _, _ = Render(c, natFacts)
		hasNot(t, "pdns scope "+ip, main.Content, "scope_zero_address")
	}
}

func TestRenderUnbound(t *testing.T) {
	main, acl, err := Render(cfg(api.RecursorUnbound, api.RecursorSettings{ECS: true, ECSZones: []string{"akamaiedge.net"}}), natFacts)
	if err != nil {
		t.Fatal(err)
	}
	if main.Path != UnboundMain || acl.Path != UnboundACL {
		t.Fatalf("paths %s %s", main.Path, acl.Path)
	}
	has(t, "unbound", main.Content, "interface: 10.0.0.43@53", "interface: 127.0.0.1@53", "do-ip6: no",
		`module-config: "subnetcache validator iterator"`, "client-subnet-zone: akamaiedge.net",
		"rrset-cache-size: 256m", "msg-cache-size: 128m", "num-threads: 2", "rrset-cache-slabs: 2",
		"remote-control:\n    control-enable: yes")
	hasNot(t, "unbound", main.Content, "send-client-subnet", "0.0.0.0@53")
	has(t, "unbound acl", acl.Content, "server:\n", "access-control: 198.51.100.99/32 allow", "access-control: ::1/128 allow")
	hasNot(t, "unbound acl", acl.Content, "0.0.0.0/0")

	main, _, _ = Render(cfg(api.RecursorUnbound, api.RecursorSettings{CacheMB: 1000}), Facts{UnboundControl: true, Threads: 3})
	has(t, "unbound plain", main.Content, "rrset-cache-size: 400m", "rrset-cache-slabs: 4")
	hasNot(t, "unbound plain", main.Content, "module-config", "remote-control:")
}

func TestRenderRejects(t *testing.T) {
	for name, c := range map[string]api.RecursorConfig{
		"kind":  cfg("bind", api.RecursorSettings{}),
		"zone":  cfg(api.RecursorPDNS, api.RecursorSettings{ECS: true, ECSZones: []string{"x'\n  evil: 1"}}),
		"cache": cfg(api.RecursorUnbound, api.RecursorSettings{CacheMB: 1}),
	} {
		if _, _, err := Render(c, natFacts); err == nil {
			t.Errorf("%s: rendered", name)
		}
	}
	if _, _, err := Render(cfg(api.RecursorPDNS, api.RecursorSettings{}), Facts{ListenIPs: []string{"0.0.0.0"}}); err == nil {
		t.Error("wildcard listen address rendered")
	}
}

func TestSafeAllow(t *testing.T) {
	got := SafeAllow([]string{"0.0.0.0/0", "1.2.3.4", "bad", "::/0", "1.2.3.4/32", "10.0.0.0/4", "198.51.100.9/24"})
	want := []string{"1.2.3.4/32", "127.0.0.0/8", "198.51.100.0/24", "::1/128"}
	if !slices.Equal(got, want) {
		t.Fatalf("SafeAllow = %v, want %v", got, want)
	}
	if got := SafeAllow(nil); !slices.Equal(got, []string{"127.0.0.0/8", "::1/128"}) {
		t.Fatalf("SafeAllow(nil) = %v", got)
	}
}
