package api

import (
	"fmt"
	"strings"
	"testing"
)

func TestRecursorSettingsValidate(t *testing.T) {
	ok := RecursorSettings{AllowExtra: []string{"203.0.113.7", "198.51.100.0/24", "2001:db8::/48", "10.0.0.0/8"},
		ECS: true, ECSZones: []string{"googlevideo.com", "akamaiedge.net"}, CacheMB: 512}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}
	if err := (RecursorSettings{}).Validate(); err != nil {
		t.Fatalf("zero settings rejected: %v", err)
	}
	many := make([]string, MaxRecursorAllow+1)
	for i := range many {
		many[i] = fmt.Sprintf("10.%d.%d.1", i/256, i%256)
	}
	for name, s := range map[string]RecursorSettings{
		"v4 any":     {AllowExtra: []string{"0.0.0.0/0"}},
		"v6 any":     {AllowExtra: []string{"::/0"}},
		"wide v4":    {AllowExtra: []string{"10.0.0.0/4"}},
		"wide v6":    {AllowExtra: []string{"2000::/3"}},
		"garbage":    {AllowExtra: []string{"nope"}},
		"zone":       {AllowExtra: []string{"fe80::1%eth0"}},
		"too many":   {AllowExtra: many},
		"bad zone":   {ECSZones: []string{"not a domain"}},
		"tiny cache": {CacheMB: 10},
		"huge cache": {CacheMB: MaxRecursorCacheMB + 1},
	} {
		if err := s.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	err := (RecursorSettings{AllowExtra: []string{"0.0.0.0/0"}}).Validate()
	if err == nil || !strings.Contains(err.Error(), "allow_extra[0]") {
		t.Errorf("error should name the field: %v", err)
	}
}

func TestAllowPrefix(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":        "203.0.113.7/32",
		"198.51.100.77/24":   "198.51.100.0/24",
		"2001:db8::1":        "2001:db8::1/128",
		"::ffff:203.0.113.7": "203.0.113.7/32",
	} {
		p, err := AllowPrefix(in)
		if err != nil || p.String() != want {
			t.Errorf("AllowPrefix(%q) = %v, %v; want %s", in, p, err, want)
		}
	}
}
