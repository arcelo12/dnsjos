package dnsconf

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/miekg/dns"

	"github.com/billyriantono/dnsjos/internal/shared/api"
)

// Live fastest-response (§6.11): every address record of a listed name is overwritten with
// the Top N measured addresses round-robin (owners stay); an answer whose measured set is
// smaller than the top N passes unchanged.
func TestLiveFastestResponse(t *testing.T) {
	d := startDnsdist(t, map[string][]string{
		"multi.example.": {"198.51.100.1", "198.51.100.2", "198.51.100.3"},
		"few.example.":   {"198.51.100.9"},
	}, func(s *api.ConfigSpec) {
		s.DualStack.Enabled = false
		s.Blocking.BlockResponseIPs = false
		s.SpeedCheck.AnswerMode, s.SpeedCheck.MaxResponse = api.AnswerModeFastestResponse, 2
	})
	os.WriteFile(filepath.Join(d.dir, FileFastestIPList),
		[]byte("multi.example 198.51.100.1 300 198.51.100.2 20 198.51.100.3 250\nfew.example 198.51.100.9 30\n"), 0o644)
	if out := d.console("fipReload()"); !strings.Contains(out, "2 names") {
		t.Fatalf("fipReload() = %s", out)
	}
	addrs := func(name string) []string {
		out := []string{}
		for _, rr := range d.query(name, dns.TypeA).Answer {
			if a, ok := rr.(*dns.A); ok {
				out = append(out, a.A.String())
			}
		}
		return out
	}
	// 3 records, top N = 2 → .2, .3, .2 (round-robin over the picks, owners unchanged)
	if got := addrs("multi.example."); !slices.Equal(got, []string{"198.51.100.2", "198.51.100.3", "198.51.100.2"}) {
		t.Errorf("multi overwrite: %v, want [.2 .3 .2]", got)
	}
	// measured set (1) < top N (2) → unchanged
	if got := addrs("few.example."); !slices.Equal(got, []string{"198.51.100.9"}) {
		t.Errorf("few must pass through: %v", got)
	}
}
