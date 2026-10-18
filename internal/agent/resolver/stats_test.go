package resolver

import (
	"testing"

	"github.com/billyriantono/dnsjos/internal/shared/api"
)

func TestParsePDNS(t *testing.T) {
	// Real `rec_control get-all` lines from PowerDNS Recursor 5.4.7.
	out := "all-outqueries\t19164\nanswers-slow\t12\npacketcache-hits\t16111\npacketcache-misses\t9503\n" +
		"qa-latency\t21679\nquestions\t25614\nservfail-answers\t3\n"
	c, lat := ParsePDNS(out)
	want := api.RecursorCounters{Queries: 25614, CacheHits: 16111, CacheMisses: 9503, Servfail: 3, Outgoing: 19164}
	if c != want || lat != 21.679 {
		t.Fatalf("ParsePDNS = %+v, %v", c, lat)
	}
}

func TestParseUnbound(t *testing.T) {
	out := "thread0.num.queries=10\ntotal.num.queries=1200\ntotal.num.cachehits=900\ntotal.num.cachemiss=300\n" +
		"total.recursion.time.avg=0.042500\nnum.answer.rcode.SERVFAIL=7\nnum.query.udpout=410\nnum.query.tcpout=5\n" +
		"garbage line\ntime.now=1760000000.123\n"
	c, lat := ParseUnbound(out)
	want := api.RecursorCounters{Queries: 1200, CacheHits: 900, CacheMisses: 300, Servfail: 7, Outgoing: 415}
	if c != want || lat != 42.5 {
		t.Fatalf("ParseUnbound = %+v, %v", c, lat)
	}
	if c, lat := ParseUnbound(""); c != (api.RecursorCounters{}) || lat != 0 {
		t.Fatalf("empty = %+v %v", c, lat)
	}
}
