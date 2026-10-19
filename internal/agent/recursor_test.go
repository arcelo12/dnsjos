package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/billyriantono/dnsjos/internal/agent/client"
	"github.com/billyriantono/dnsjos/internal/agent/resolver"
	"github.com/billyriantono/dnsjos/internal/shared/api"
)

// fakeSystem records commands; "systemctl is-active" answers active unless down.
type fakeSystem struct {
	mu   sync.Mutex
	cmds []string
	down bool // the resolver does not come back after a restart
}

func (f *fakeSystem) run(_ context.Context, _ []string, name string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := strings.Join(append([]string{name}, args...), " ")
	f.cmds = append(f.cmds, c)
	switch {
	case c == "systemctl is-active pdns-recursor" && f.down:
		return "failed", errors.New("exit status 3")
	case strings.HasPrefix(c, "systemctl is-active"):
		return "active", nil
	case c == "rec_control get-all":
		return "questions\t10\npacketcache-hits\t4\npacketcache-misses\t6\nqa-latency\t1500\n", nil
	}
	return "", nil
}

func (f *fakeSystem) ran(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.ContainsFunc(f.cmds, func(c string) bool { return strings.HasPrefix(c, prefix) })
}

func (f *fakeSystem) reset() {
	f.mu.Lock()
	f.cmds = nil
	f.mu.Unlock()
}

func testRecAgent(t *testing.T, panel string) (*recAgent, *fakeSystem) {
	t.Helper()
	sys := &fakeSystem{}
	o := Options{Root: t.TempDir(), Version: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a := &recAgent{o: o, cl: client.New(panel, "rtok", "test"), kind: api.RecursorPDNS, run: sys.run, sleep: func(time.Duration) {},
		facts: func() resolver.Facts {
			return resolver.Facts{ListenIPs: []string{"10.0.0.43"}, Threads: 2, MemMB: 4096}
		},
		probe:   func(context.Context) (time.Duration, error) { return 3 * time.Millisecond, nil },
		pollNow: make(chan struct{}, 1), forceNow: make(chan struct{}, 1), hbNow: make(chan struct{}, 1), start: time.Now()}
	return a, sys
}

func recCfg(allow ...string) *api.RecursorConfig {
	return &api.RecursorConfig{Version: 1, Kind: api.RecursorPDNS, PublicIP: "192.0.2.53", Allow: allow,
		Settings: api.RecursorSettings{ECS: true}}
}

func readRec(t *testing.T, a *recAgent, p string) string {
	t.Helper()
	b, err := os.ReadFile(a.o.path(p))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRecursorApplyACLOnly(t *testing.T) {
	a, sys := testRecAgent(t, "http://unused")
	ctx := context.Background()
	if changed, err := a.apply(ctx, recCfg("198.51.100.10/32"), false); err != nil || !changed {
		t.Fatalf("first apply: %v %v", changed, err)
	}
	if !sys.ran("systemctl restart pdns-recursor") {
		t.Fatalf("first apply did not restart: %v", sys.cmds)
	}
	if !strings.Contains(readRec(t, a, resolver.PDNSMain), "scope_zero_address: 192.0.2.53") {
		t.Fatal("main config not written")
	}
	sys.reset()
	if changed, err := a.apply(ctx, recCfg("198.51.100.10/32"), false); err != nil || changed {
		t.Fatalf("same config: changed=%v err=%v", changed, err)
	}
	if len(sys.cmds) != 0 {
		t.Fatalf("unchanged config ran %v", sys.cmds)
	}
	// A node joined: only the allow-list file changes, so the ACLs are reloaded in place.
	if _, err := a.apply(ctx, recCfg("198.51.100.10/32", "203.0.113.5/32"), false); err != nil {
		t.Fatal(err)
	}
	if !sys.ran("rec_control reload-acls") || sys.ran("systemctl restart") {
		t.Fatalf("allow-list change ran %v", sys.cmds)
	}
	if acl := readRec(t, a, resolver.PDNSACL); !strings.Contains(acl, "203.0.113.5/32") || strings.Contains(acl, "0.0.0.0/0") {
		t.Fatalf("acl file:\n%s", acl)
	}
}

func TestRecursorApplyRollback(t *testing.T) {
	a, sys := testRecAgent(t, "http://unused")
	ctx := context.Background()
	if _, err := a.apply(ctx, recCfg("198.51.100.10/32"), false); err != nil {
		t.Fatal(err)
	}
	good := readRec(t, a, resolver.PDNSMain)
	sys.down = true
	c := recCfg("198.51.100.10/32")
	c.Settings.CacheMB = 512 // main file changes → restart, which fails
	_, err := a.apply(ctx, c, false)
	if err == nil || !strings.Contains(err.Error(), "previous config restored") {
		t.Fatalf("apply with a failing resolver: %v", err)
	}
	if got := readRec(t, a, resolver.PDNSMain); got != good {
		t.Fatalf("previous config not restored:\n%s", got)
	}
	// An invalid config is never written.
	sys.down = false
	bad := recCfg()
	bad.Settings.ECSZones = []string{"bad zone"}
	if _, err := a.apply(ctx, bad, false); err == nil {
		t.Fatal("invalid settings applied")
	}
	if got := readRec(t, a, resolver.PDNSMain); got != good {
		t.Fatal("invalid settings overwrote the config")
	}
}

func TestRecursorConfigAndHeartbeat(t *testing.T) {
	var mu sync.Mutex
	var hbs []api.RecursorHeartbeat
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer rtok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/agent/v1/recursor/config":
			if r.Header.Get("If-None-Match") == `"1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			json.NewEncoder(w).Encode(recCfg("198.51.100.10/32"))
		case "/agent/v1/recursor/heartbeat":
			var hb api.RecursorHeartbeat
			json.NewDecoder(r.Body).Decode(&hb)
			mu.Lock()
			hbs = append(hbs, hb)
			mu.Unlock()
			json.NewEncoder(w).Encode(api.RecursorAck{ConfigVersion: 1, Commands: []api.Command{{ID: 9, Type: api.CmdRestartResolver}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer panel.Close()
	a, sys := testRecAgent(t, panel.URL)
	ctx := context.Background()
	a.pollConfig(ctx, false)
	if a.applied != 1 || a.applyErr != "" {
		t.Fatalf("applied=%d err=%q", a.applied, a.applyErr)
	}
	sys.reset()
	a.pollConfig(ctx, false) // 304
	if sys.ran("systemctl restart") {
		t.Fatal("a 304 restarted the resolver")
	}
	a.heartbeat(ctx)
	a.ops.Wait()
	a.heartbeat(ctx)
	mu.Lock()
	defer mu.Unlock()
	if len(hbs) != 2 {
		t.Fatalf("%d heartbeats", len(hbs))
	}
	hb := hbs[0]
	if hb.AppliedConfigVersion != 1 || !hb.ResolverRunning || !hb.ProbeOK || hb.ListenIP != "10.0.0.43" ||
		hb.Counters.Queries != 10 || hb.Counters.CacheHits != 4 || hb.LatencyAvgMs != 1.5 {
		t.Fatalf("heartbeat %+v", hb)
	}
	if !sys.ran("systemctl restart pdns-recursor") {
		t.Fatal("restart_resolver command not run")
	}
	if !slices.Equal(hbs[1].AckedCommands, []int64{9}) {
		t.Fatalf("command not acked: %v", hbs[1].AckedCommands)
	}
}

func TestEnrollRecursorWritesRole(t *testing.T) {
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req api.RecursorEnrollRequest
		json.NewDecoder(r.Body).Decode(&req)
		if r.URL.Path != "/agent/v1/recursor/enroll" || req.Token != "etok" || req.Kind != api.RecursorUnbound {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(api.RecursorEnrollResponse{RecursorID: "r1", Name: "rec1", Kind: api.RecursorUnbound, Token: "rtok"})
	}))
	defer panel.Close()
	o := Options{Root: t.TempDir(), Version: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := EnrollRecursor(context.Background(), o, panel.URL, "etok", "bind", ""); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if err := EnrollRecursor(context.Background(), o, panel.URL, "etok", api.RecursorUnbound, "rec1"); err != nil {
		t.Fatal(err)
	}
	if !IsRecursor(o) {
		t.Fatal("not a recursor after enroll")
	}
	c, _ := loadConfig(filepath.Join(o.Root, ConfigPath))
	if c.NodeID != "r1" || c.NodeToken != "rtok" || c.Kind != api.RecursorUnbound {
		t.Fatalf("agent.json %+v", c)
	}
}

func TestRecursorReappliesOnHostChangeOnly(t *testing.T) {
	var gets int
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets++
		if r.Header.Get("If-None-Match") == `"1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		json.NewEncoder(w).Encode(recCfg("198.51.100.10/32"))
	}))
	defer panel.Close()
	a, sys := testRecAgent(t, panel.URL)
	listen := "10.0.0.43"
	a.facts = func() resolver.Facts { return resolver.Facts{ListenIPs: []string{listen}, Threads: 1, MemMB: 1024} }
	ctx := context.Background()
	a.pollConfig(ctx, false)
	// The address came up late / changed: a 304 still re-renders and restarts on it.
	listen = "10.0.0.44"
	sys.reset()
	a.pollConfig(ctx, false)
	if !sys.ran("systemctl restart pdns-recursor") || !strings.Contains(readRec(t, a, resolver.PDNSMain), "10.0.0.44:53") {
		t.Fatalf("listen address change not applied: %v", sys.cmds)
	}
	sys.reset()
	a.pollConfig(ctx, false) // 304, nothing changed: nothing runs
	if len(sys.cmds) != 0 {
		t.Fatalf("unchanged poll ran %v", sys.cmds)
	}
	// No network yet: nothing is written, the running config stays.
	listen = ""
	a.facts = func() resolver.Facts { return resolver.Facts{Threads: 1, MemMB: 1024} }
	a.pollConfig(ctx, false)
	if a.applyErr == "" || !strings.Contains(readRec(t, a, resolver.PDNSMain), "10.0.0.44:53") || len(sys.cmds) != 0 {
		t.Fatalf("no-address poll: err=%q cmds=%v", a.applyErr, sys.cmds)
	}
}

func TestRecursorFailedConfigNotRetriedEveryPoll(t *testing.T) {
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		json.NewEncoder(w).Encode(recCfg("198.51.100.10/32"))
	}))
	defer panel.Close()
	a, sys := testRecAgent(t, panel.URL)
	sys.down = true
	ctx := context.Background()
	a.pollConfig(ctx, false)
	if a.applyErr == "" {
		t.Fatal("failing resolver not reported")
	}
	sys.reset()
	a.pollConfig(ctx, false)
	if sys.ran("systemctl restart") || a.applyErr == "" {
		t.Fatalf("failed config retried on a plain poll: %v (err %q)", sys.cmds, a.applyErr)
	}
	sys.down = false
	a.pollConfig(ctx, true) // reapply
	if a.applyErr != "" || a.applied != 1 {
		t.Fatalf("reapply after the fix: applied=%d err=%q", a.applied, a.applyErr)
	}
}

func TestRecursorSecondUpgradeDropped(t *testing.T) {
	var downloads int
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer panel.Close()
	a, _ := testRecAgent(t, panel.URL)
	a.upgrading = true // one is running
	a.command(context.Background(), api.Command{ID: 7, Type: api.CmdUpgradeAgent})
	a.ops.Wait()
	if downloads != 0 || !slices.Equal(a.acks, []int64{7}) {
		t.Fatalf("second upgrade: downloads=%d acks=%v", downloads, a.acks)
	}
	a.upgrading = false
	a.command(context.Background(), api.Command{ID: 8, Type: api.CmdUpgradeAgent})
	a.ops.Wait()
	if downloads == 0 || a.upgrading || a.lastUp == nil || a.lastUp.Error == "" {
		t.Fatalf("failed upgrade: downloads=%d upgrading=%v last=%+v", downloads, a.upgrading, a.lastUp)
	}
}
