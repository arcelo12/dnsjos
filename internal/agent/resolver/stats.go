package resolver

import (
	"strconv"
	"strings"

	"github.com/billyriantono/dnsjos/internal/shared/api"
)

// ParsePDNS reads `rec_control get-all` ("name<TAB>value" lines). The cache counters are
// the packet cache (whole answers served without recursion); latency is qa-latency, the
// recursor's moving average answer time in microseconds, returned in ms.
func ParsePDNS(out string) (api.RecursorCounters, float64) {
	m := fields(out, "\t ")
	c := api.RecursorCounters{
		Queries: m["questions"], CacheHits: m["packetcache-hits"], CacheMisses: m["packetcache-misses"],
		Servfail: m["servfail-answers"], Outgoing: m["all-outqueries"],
	}
	return c, float64(m["qa-latency"]) / 1000
}

// ParseUnbound reads `unbound-control stats_noreset` ("name=value" lines). Latency is
// the average recursion time of cache misses (seconds), returned in ms.
func ParseUnbound(out string) (api.RecursorCounters, float64) {
	m := fields(out, "=")
	c := api.RecursorCounters{
		Queries: m["total.num.queries"], CacheHits: m["total.num.cachehits"], CacheMisses: m["total.num.cachemiss"],
		Servfail: m["num.answer.rcode.SERVFAIL"], Outgoing: m["num.query.udpout"] + m["num.query.tcpout"],
	}
	var lat float64
	for l := range strings.SplitSeq(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "total.recursion.time.avg="); ok {
			lat, _ = strconv.ParseFloat(v, 64)
		}
	}
	return c, lat * 1000
}

// fields maps "name<sep>value" lines to integer values (fractions truncated, junk skipped).
func fields(out, seps string) map[string]int64 {
	m := map[string]int64{}
	for l := range strings.SplitSeq(out, "\n") {
		i := strings.IndexAny(l, seps)
		if i < 1 {
			continue
		}
		v := strings.TrimSpace(strings.TrimLeft(l[i:], seps))
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			m[l[:i]] = n
		} else if f, err := strconv.ParseFloat(v, 64); err == nil {
			m[l[:i]] = int64(f)
		}
	}
	return m
}
