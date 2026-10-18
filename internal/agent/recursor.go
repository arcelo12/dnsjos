package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/billyriantono/dnsjos/internal/agent/apply"
	"github.com/billyriantono/dnsjos/internal/agent/client"
	"github.com/billyriantono/dnsjos/internal/agent/collect"
	"github.com/billyriantono/dnsjos/internal/agent/resolver"
	"github.com/billyriantono/dnsjos/internal/agent/speedcheck"
	"github.com/billyriantono/dnsjos/internal/shared/api"
)

// Recursor mode (SPEC §20): the agent manages PowerDNS Recursor or Unbound on a recursor
// host instead of dnsdist. agent.json has role "recursor".
const RoleRecursor = "recursor"

// IsRecursor reports whether this host is enrolled as a recursor.
func IsRecursor(o Options) bool {
	c, err := loadConfig(o.path(ConfigPath))
	return err == nil && c.Role == RoleRecursor
}

// EnrollRecursor exchanges a recursor token for the recursor's own token and writes
// agent.json. kind is the resolver the installer set up; the panel checks it against the token.
func EnrollRecursor(ctx context.Context, o Options, panelURL, token, kind, name string) error {
	if panelURL == "" || token == "" {
		return errors.New("enroll: --panel and --token are required")
	}
	if !slices.Contains(api.RecursorKinds, kind) {
		return fmt.Errorf("enroll --recursor: --kind must be one of %s", strings.Join(api.RecursorKinds, ", "))
	}
	host := name
	if host == "" {
		host, _ = os.Hostname()
	}
	cl := client.New(panelURL, "", o.Version)
	resp, err := cl.EnrollRecursor(ctx, api.RecursorEnrollRequest{Token: token, Hostname: host, Kind: kind, OS: osName(),
		Arch: runtime.GOARCH, AgentVersion: o.Version, ResolverVersion: resolverVersion(ctx, execRunner, kind)})
	if err != nil {
		return fmt.Errorf("enroll: %w", err)
	}
	b, _ := json.MarshalIndent(Config{PanelURL: cl.BaseURL, NodeID: resp.RecursorID, NodeToken: resp.Token,
		Role: RoleRecursor, Kind: resp.Kind}, "", "  ")
	if err := writePrivate(o.path(ConfigPath), b); err != nil {
		return err
	}
	o.Log.Info("enrolled as recursor", "recursor_id", resp.RecursorID, "name", resp.Name, "kind", resp.Kind, "panel", cl.BaseURL)
	return nil
}

// resolverVersion is the installed package version of the resolver ("" when unknown).
func resolverVersion(ctx context.Context, run Runner, kind string) string {
	v, err := run(ctx, nil, "dpkg-query", "-W", "-f=${Version}", resolver.ServiceName(kind))
	if err != nil {
		return ""
	}
	return v
}

type recAgent struct {
	o     Options
	cl    *client.Client
	kind  string
	run   Runner
	os    string
	start time.Time
	stop  context.CancelCauseFunc
	// Test hooks: host facts, the test lookup through the resolver and the waits of
	// the service checks.
	facts func() resolver.Facts
	probe func(context.Context) (time.Duration, error)
	sleep func(time.Duration)

	opMu sync.Mutex     // serialises applies, restarts and the agent upgrade
	ops  sync.WaitGroup // command goroutines; RunRecursor waits for them

	mu       sync.Mutex
	cfg      *api.RecursorConfig
	seen     int // version of cfg (sent as ETag)
	applied  int
	applyErr string
	resVer   string
	acks     []int64
	marker   *agentMarker
	lastUp   *api.UpgradeResult

	pollNow, forceNow, hbNow chan struct{}
}

// RunRecursor runs the recursor agent until ctx is cancelled.
func RunRecursor(ctx context.Context, o Options) error {
	if err := upgradeGuard(o); err != nil {
		return err
	}
	a, err := newRecAgent(ctx, o)
	if err != nil {
		return err
	}
	ctx, a.stop = context.WithCancelCause(ctx)
	o.Log.Info("recursor agent starting", "version", o.Version, "kind", a.kind, "panel", a.cl.BaseURL, "root", o.Root)
	var wg sync.WaitGroup
	for _, f := range []func(context.Context){a.configLoop, a.heartbeatLoop} {
		wg.Add(1)
		go func() { defer wg.Done(); f(ctx) }()
	}
	wg.Wait()
	a.ops.Wait()
	if err := context.Cause(ctx); errors.Is(err, ErrRestart) {
		return err
	}
	o.Log.Info("recursor agent stopped")
	return nil
}

func newRecAgent(ctx context.Context, o Options) (*recAgent, error) {
	c, err := loadConfig(o.path(ConfigPath))
	if err != nil {
		return nil, err
	}
	if c.Role != RoleRecursor || !slices.Contains(api.RecursorKinds, c.Kind) {
		return nil, fmt.Errorf("%s: not a recursor enrollment (role %q, kind %q)", o.path(ConfigPath), c.Role, c.Kind)
	}
	if err := os.MkdirAll(o.path(DataDir), 0o711); err != nil {
		return nil, err
	}
	a := &recAgent{o: o, cl: client.New(c.PanelURL, c.NodeToken, o.Version), kind: c.Kind, run: execRunner,
		os: osName(), start: time.Now(), sleep: time.Sleep,
		pollNow: make(chan struct{}, 1), forceNow: make(chan struct{}, 1), hbNow: make(chan struct{}, 1)}
	a.facts, a.probe = a.hostFacts, probeResolver
	a.marker, a.lastUp = loadUpgradeState(o)
	a.resVer = resolverVersion(ctx, a.run, a.kind)
	return a, nil
}

// hostFacts: the resolver answers on the local IPv4 holding the default route (behind
// NAT that is the private address; the panel knows the public one) and on the global
// IPv6 source address when there is one.
func (a *recAgent) hostFacts() resolver.Facts {
	f := resolver.Facts{Threads: min(runtime.NumCPU(), 8), MemMB: collect.System(a.o.path(DataDir)).MemTotalMB}
	if ip := localIPv4(); ip.IsValid() {
		f.ListenIPs = append(f.ListenIPs, ip.String())
	}
	if ip := speedcheck.OutboundIPv6(); ip.IsGlobalUnicast() {
		f.ListenIPs = append(f.ListenIPs, ip.String())
		f.IPv6 = true
	}
	_, err := os.Stat(a.o.path(resolver.UnboundCtl))
	f.UnboundControl = err == nil
	return f
}

// localIPv4 is the source address the kernel picks for IPv4 traffic to the internet.
func localIPv4() netip.Addr {
	c, err := net.Dial("udp4", "1.1.1.1:53") // no packet is sent
	if err != nil {
		return netip.Addr{}
	}
	defer c.Close()
	ap, _ := netip.ParseAddrPort(c.LocalAddr().String())
	if a := ap.Addr(); a.IsValid() && !a.IsLoopback() && !a.IsUnspecified() {
		return a
	}
	return netip.Addr{}
}

// probeResolver resolves a.root-servers.net through the local resolver.
func probeResolver(ctx context.Context) (time.Duration, error) {
	m := new(dns.Msg)
	m.SetQuestion("a.root-servers.net.", dns.TypeA)
	c := dns.Client{Timeout: 2 * time.Second}
	r, rtt, err := c.ExchangeContext(ctx, m, resolver.ProbeAddress)
	if err != nil {
		return 0, err
	}
	if r.Rcode != dns.RcodeSuccess {
		return rtt, fmt.Errorf("test lookup a.root-servers.net: %s", dns.RcodeToString[r.Rcode])
	}
	return rtt, nil
}

// ── config ──────────────────────────────────────────────────────────────────

func (a *recAgent) config() *api.RecursorConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

func (a *recAgent) configLoop(ctx context.Context) {
	interval := func() time.Duration {
		if c := a.config(); c != nil && c.PollIntervalS > 0 {
			return time.Duration(c.PollIntervalS) * time.Second
		}
		return api.DefaultPollS * time.Second
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		force := false
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-a.pollNow:
		case <-a.forceNow:
			force = true
		}
		a.pollConfig(ctx, force)
		t.Reset(interval())
	}
}

func (a *recAgent) pollConfig(ctx context.Context, force bool) {
	a.mu.Lock()
	ver := a.seen
	a.mu.Unlock()
	if force {
		ver = 0
	}
	cfg, err := a.cl.RecursorConfig(ctx, ver)
	if err != nil {
		if ctx.Err() == nil {
			a.o.Log.Warn("fetching config failed", "err", err)
		}
		return
	}
	if cfg == nil { // 304: unchanged (a forced poll sends no ETag, so it never gets one)
		return
	}
	a.mu.Lock()
	a.cfg, a.seen = cfg, cfg.Version
	a.mu.Unlock()
	a.opMu.Lock()
	changed, err := a.apply(ctx, cfg, force)
	a.opMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.applyErr = fmt.Sprintf("config v%d: %v", cfg.Version, err)
		a.o.Log.Error("config apply failed", "version", cfg.Version, "err", err)
		return
	}
	a.applied, a.applyErr = cfg.Version, ""
	if changed {
		a.resVer = resolverVersion(ctx, a.run, a.kind)
		a.o.Log.Info("config applied", "version", cfg.Version, "allow", len(cfg.Allow), "ecs", cfg.Settings.ECS)
		poke(a.hbNow)
	}
}

// apply writes the rendered files and makes the resolver use them: a reload when only
// the allow-list changed, else a restart. When the resolver does not come back, the
// previous files are restored and it is restarted on them (the error says so).
func (a *recAgent) apply(ctx context.Context, cfg *api.RecursorConfig, force bool) (bool, error) {
	main, acl, err := resolver.Render(*cfg, a.facts())
	if err != nil {
		return false, err // nothing written: the resolver keeps its config
	}
	files := []resolver.File{main, acl}
	old := make([][]byte, len(files))
	changed := make([]bool, len(files))
	for i, f := range files {
		b, err := os.ReadFile(a.o.path(f.Path))
		if err != nil && !os.IsNotExist(err) {
			return false, err
		}
		old[i], changed[i] = b, err != nil || !bytes.Equal(b, []byte(f.Content))
	}
	if !changed[0] && !changed[1] && !force {
		return false, nil
	}
	restore := func() {
		for i, f := range files {
			if old[i] == nil {
				os.Remove(a.o.path(f.Path))
			} else {
				apply.WriteFile(a.o.path(f.Path), old[i], 0o644)
			}
		}
	}
	for i, f := range files {
		if !changed[i] {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(a.o.path(f.Path)), 0o755); err != nil {
			return false, err
		}
		if err := apply.WriteFile(a.o.path(f.Path), []byte(f.Content), 0o644); err != nil {
			restore()
			return false, err
		}
	}
	if err := a.activate(ctx, changed[0] || force); err != nil {
		restore()
		if rerr := a.restartResolver(ctx); rerr != nil {
			return false, fmt.Errorf("%w; previous config restored but the resolver did not start: %v", err, rerr)
		}
		return false, fmt.Errorf("%w; previous config restored", err)
	}
	return true, nil
}

// activate makes the resolver load the new files.
func (a *recAgent) activate(ctx context.Context, restart bool) error {
	if a.o.NoSystemd {
		return nil
	}
	if a.kind == api.RecursorUnbound {
		if _, err := a.run(ctx, nil, "unbound-checkconf"); err != nil {
			return err
		}
	}
	if !restart {
		var err error
		if a.kind == api.RecursorPDNS {
			_, err = a.run(ctx, nil, "rec_control", "reload-acls")
		} else if _, err = a.run(ctx, nil, "unbound-control", "reload_keep_cache"); err != nil {
			_, err = a.run(ctx, nil, "unbound-control", "reload") // Unbound < 1.18
		}
		if err == nil {
			return nil
		}
		a.o.Log.Warn("resolver reload failed, restarting it", "err", err)
	}
	return a.restartResolver(ctx)
}

// restartResolver restarts the service and waits until it stays up.
func (a *recAgent) restartResolver(ctx context.Context) error {
	if a.o.NoSystemd {
		return nil
	}
	svc := resolver.ServiceName(a.kind)
	if _, err := a.run(ctx, nil, "systemctl", "restart", svc); err != nil {
		return err
	}
	for i := 0; ; i++ {
		a.sleep(500 * time.Millisecond)
		if a.running(ctx) {
			a.sleep(2 * time.Second) // a resolver that exits right after starting is not up
			if a.running(ctx) {
				return nil
			}
		}
		if i >= 20 || ctx.Err() != nil {
			out, _ := a.run(ctx, nil, "systemctl", "is-active", svc)
			return fmt.Errorf("%s is not running after the restart (%s): journalctl -u %s", svc, strings.TrimSpace(out), svc)
		}
	}
}

func (a *recAgent) running(ctx context.Context) bool {
	if a.o.NoSystemd {
		return true
	}
	out, err := a.run(ctx, nil, "systemctl", "is-active", resolver.ServiceName(a.kind))
	return err == nil && strings.TrimSpace(out) == "active"
}

// ── heartbeat + commands ────────────────────────────────────────────────────

func (a *recAgent) heartbeatLoop(ctx context.Context) {
	interval := func() time.Duration {
		if c := a.config(); c != nil && c.HeartbeatIntervalS > 0 {
			return time.Duration(c.HeartbeatIntervalS) * time.Second
		}
		return api.DefaultHeartbS * time.Second
	}
	every(ctx, interval, a.hbNow, func() { a.heartbeat(ctx) })
}

func (a *recAgent) collect(ctx context.Context) *api.RecursorHeartbeat {
	hb := &api.RecursorHeartbeat{Time: time.Now().UTC(), AgentVersion: a.o.Version, OS: a.os,
		UptimeS: int64(time.Since(a.start).Seconds()), System: collect.System(a.o.path(DataDir)),
		ResolverRunning: a.running(ctx)}
	for _, s := range a.facts().ListenIPs {
		if ip, err := netip.ParseAddr(s); err == nil && ip.Is4() && hb.ListenIP == "" {
			hb.ListenIP = s
		} else if err == nil && ip.Is6() && hb.ListenIPv6 == "" {
			hb.ListenIPv6 = s
		}
	}
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	rtt, err := a.probe(pctx)
	cancel()
	hb.ProbeOK, hb.ProbeMs = err == nil, float64(rtt.Microseconds())/1000
	if err != nil {
		hb.ProbeError = err.Error()
	}
	if !a.o.NoSystemd {
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		var out string
		if a.kind == api.RecursorPDNS {
			out, err = a.run(sctx, nil, "rec_control", "get-all")
			hb.Counters, hb.LatencyAvgMs = resolver.ParsePDNS(out)
		} else {
			out, err = a.run(sctx, nil, "unbound-control", "stats_noreset")
			hb.Counters, hb.LatencyAvgMs = resolver.ParseUnbound(out)
		}
		cancel()
		if err != nil {
			hb.Counters, hb.LatencyAvgMs, hb.StatsError = api.RecursorCounters{}, 0, err.Error()
		}
	}
	a.mu.Lock()
	hb.ResolverVersion, hb.AppliedConfigVersion, hb.ApplyError = a.resVer, a.applied, a.applyErr
	hb.AckedCommands, hb.LastUpgrade = slices.Clone(a.acks), a.lastUp
	a.mu.Unlock()
	return hb
}

func (a *recAgent) heartbeat(ctx context.Context) {
	hb := a.collect(ctx)
	a.mu.Lock()
	seen := a.seen
	a.mu.Unlock()
	ack, err := a.cl.RecursorHeartbeat(ctx, hb)
	if err != nil {
		if ctx.Err() == nil {
			a.o.Log.Warn("heartbeat failed", "err", err)
		}
		return
	}
	a.mu.Lock()
	m := a.marker
	a.marker = nil
	a.acks = slices.DeleteFunc(a.acks, func(id int64) bool { return slices.Contains(hb.AckedCommands, id) })
	a.mu.Unlock()
	if m != nil {
		res := confirmAgentUpgrade(a.o, m)
		a.mu.Lock()
		a.lastUp = &res
		a.mu.Unlock()
		poke(a.hbNow)
	}
	if ack.ConfigVersion != 0 && ack.ConfigVersion != seen {
		poke(a.pollNow)
	}
	for _, c := range ack.Commands {
		a.command(ctx, c)
	}
}

func (a *recAgent) command(ctx context.Context, c api.Command) {
	a.o.Log.Info("command received", "id", c.ID, "type", c.Type)
	// Acked with the next heartbeat; queued first so the upgrade's own heartbeat carries it.
	a.mu.Lock()
	a.acks = append(a.acks, c.ID)
	a.mu.Unlock()
	switch c.Type {
	case api.CmdReapply:
		poke(a.forceNow)
	case api.CmdRestartResolver:
		a.ops.Go(func() {
			a.opMu.Lock()
			defer a.opMu.Unlock()
			if err := a.restartResolver(ctx); err != nil {
				a.o.Log.Error("restarting the resolver failed", "err", err)
			}
		})
	case api.CmdUpgradeAgent:
		a.ops.Go(func() {
			if err := replaceAgentBinary(ctx, a.o, a.cl); err != nil {
				a.mu.Lock()
				a.lastUp = &api.UpgradeResult{Kind: api.UpgradeAgent, From: a.o.Version, Error: err.Error(), At: time.Now().UTC()}
				a.mu.Unlock()
				a.o.Log.Error("agent upgrade failed", "err", err)
				poke(a.hbNow)
				return
			}
			a.o.Log.Info("agent binary replaced, restarting")
			a.heartbeat(ctx) // ack the command before exiting
			a.opMu.Lock()    // let a running apply finish first
			a.stop(ErrRestart)
			a.opMu.Unlock()
		})
	default:
		a.o.Log.Warn("unknown command", "type", c.Type)
	}
}
