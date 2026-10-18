package recursors

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/billyriantono/dnsjos/internal/panel/app"
	"github.com/billyriantono/dnsjos/internal/panel/db/dbtest"
	"github.com/billyriantono/dnsjos/internal/panel/jobs"
	"github.com/billyriantono/dnsjos/internal/panel/nodes"
	"github.com/billyriantono/dnsjos/internal/panel/reports"
	"github.com/billyriantono/dnsjos/internal/shared/api"
)

type env struct {
	t      *testing.T
	pool   *pgxpool.Pool
	srv    *httptest.Server
	cookie string
}

func setup(t *testing.T) *env {
	pool := dbtest.New(t, "recursors")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := &app.Deps{Pool: pool, Log: log, Live: app.NewLiveStore(), Settings: app.NewSettings(pool, "http://panel.test:8080"),
		Jobs: jobs.New(ctx, log), AgentVersion: "v2"}
	if err := d.Settings.Load(ctx); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, pool: pool, cookie: app.NewToken()}
	spec := api.DefaultConfigSpec()
	spec.Upstreams.Servers = []api.Upstream{{Address: "192.0.2.53:53", Weight: 1, Order: 1, Sockets: 1, ECS: true}}
	b, _ := json.Marshal(spec)
	e.run(`INSERT INTO config_versions (profile_id, version, spec, published, published_at)
		SELECT id, 1, $1, true, now() FROM config_profiles WHERE name = 'default'`, b)
	e.run(`WITH u AS (INSERT INTO users (email, password_hash, role) VALUES ('a@example.com', 'x', 'admin') RETURNING id)
		INSERT INTO sessions (id_hash, user_id, expires_at) SELECT $1, id, now() + interval '1 hour' FROM u`,
		app.HashToken(e.cookie))
	r := app.NewRouter(d)
	Register(r, d)
	nodes.Register(r, d)
	reports.Register(r, d)
	e.srv = httptest.NewServer(r)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) run(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

// call sends a request (auth "" = none, "admin" = session, else a bearer token), expects
// status want and decodes the JSON body into out.
func (e *env) call(want int, method, path, auth string, body, out any) *http.Response {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	switch auth {
	case "":
	case "admin":
		req.AddCookie(&http.Cookie{Name: app.SessionCookie, Value: e.cookie})
	default:
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		e.t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, want, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			e.t.Fatalf("%s %s: %v: %s", method, path, err, b)
		}
	}
	return resp
}

func (e *env) enrollRecursor(name, kind string) api.RecursorEnrollResponse {
	e.t.Helper()
	var tok api.RecursorTokenCreated
	e.call(201, "POST", "/api/v1/recursor-tokens", "admin", api.RecursorTokenCreate{Name: name, Kind: kind, TTLHours: 1}, &tok)
	var er api.RecursorEnrollResponse
	e.call(200, "POST", "/agent/v1/recursor/enroll", "", api.RecursorEnrollRequest{Token: tok.Token, Hostname: "h-" + name,
		Kind: kind, OS: "debian 13", Arch: "amd64", AgentVersion: "v1", PublicIP: "192.0.2.53"}, &er)
	return er
}

func (e *env) enrollNode(name, ip string) api.EnrollResponse {
	e.t.Helper()
	var tok api.EnrollmentTokenCreated
	e.call(201, "POST", "/api/v1/enrollment-tokens", "admin", api.EnrollmentTokenCreate{NodeName: name, TTLHours: 1}, &tok)
	var er api.EnrollResponse
	e.call(200, "POST", "/agent/v1/enroll", "", api.EnrollRequest{Token: tok.Token, Hostname: name, PublicIP: ip}, &er)
	return er
}

func TestRecursorLifecycle(t *testing.T) {
	e := setup(t)
	node := e.enrollNode("ns4", "198.51.100.99")

	var tok api.RecursorTokenCreated
	e.call(201, "POST", "/api/v1/recursor-tokens", "admin", api.RecursorTokenCreate{Name: "rec1", Kind: "pdns"}, &tok)
	want := "curl -fsSL http://panel.test:8080/install-recursor.sh | sudo sh -s -- --token " + tok.Token + " --kind pdns"
	if tok.InstallCommand != want {
		t.Errorf("install command %q", tok.InstallCommand)
	}
	e.call(400, "POST", "/api/v1/recursor-tokens", "admin", api.RecursorTokenCreate{Kind: "bind"}, nil)
	e.call(400, "POST", "/api/v1/recursor-tokens", "admin", api.RecursorTokenCreate{Name: "bad name", Kind: "pdns"}, nil)
	// The installer's kind must match the token's; a mismatch does not use the token up.
	e.call(400, "POST", "/agent/v1/recursor/enroll", "", api.RecursorEnrollRequest{Token: tok.Token, Kind: "unbound"}, nil)
	var rec api.RecursorEnrollResponse
	e.call(200, "POST", "/agent/v1/recursor/enroll", "", api.RecursorEnrollRequest{Token: tok.Token, Hostname: "h1",
		Kind: "pdns", AgentVersion: "v1", PublicIP: "192.0.2.53"}, &rec)
	if rec.Kind != "pdns" || rec.Name != "rec1" || rec.Token == "" {
		t.Fatalf("enroll %+v", rec)
	}
	e.call(401, "POST", "/agent/v1/recursor/enroll", "", api.RecursorEnrollRequest{Token: tok.Token, Kind: "pdns"}, nil) // used

	// Tokens never cross: a recursor token does not open node routes and vice versa.
	e.call(401, "GET", "/agent/v1/config", rec.Token, nil, nil)
	e.call(401, "GET", "/agent/v1/recursor/config", node.NodeToken, nil, nil)
	e.call(401, "GET", "/agent/v1/recursor/config", "", nil, nil)

	var cfg api.RecursorConfig
	e.call(200, "GET", "/agent/v1/recursor/config", rec.Token, nil, &cfg)
	if cfg.Kind != "pdns" || cfg.PublicIP != "192.0.2.53" || !cfg.Settings.ECS || cfg.Version == 0 {
		t.Fatalf("config %+v", cfg)
	}
	if want := []string{"127.0.0.0/8", "198.51.100.99/32", "::1/128"}; !slices.Equal(cfg.Allow, want) {
		t.Fatalf("allow %v, want %v", cfg.Allow, want)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/agent/v1/recursor/config", nil)
	req.Header.Set("Authorization", "Bearer "+rec.Token)
	req.Header.Set("If-None-Match", `"`+itoa(cfg.Version)+`"`)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 304 {
		t.Fatalf("ETag: %v %v", resp.StatusCode, err)
	}

	// Heartbeats: online, rates and metrics from the counter deltas.
	now := time.Now().UTC()
	hb := api.RecursorHeartbeat{Time: now.Add(-10 * time.Second), AgentVersion: "v1", ResolverRunning: true, ProbeOK: true,
		AppliedConfigVersion: cfg.Version, ListenIP: "10.0.0.43", Counters: api.RecursorCounters{Queries: 1000, CacheHits: 600, CacheMisses: 400}}
	var ack api.RecursorAck
	e.call(200, "POST", "/agent/v1/recursor/heartbeat", rec.Token, hb, &ack)
	if ack.ConfigVersion != cfg.Version {
		t.Fatalf("ack version %d, want %d", ack.ConfigVersion, cfg.Version)
	}
	hb.Time, hb.LatencyAvgMs = now, 12
	hb.Counters = api.RecursorCounters{Queries: 1100, CacheHits: 680, CacheMisses: 420, Servfail: 2, Outgoing: 30}
	e.call(200, "POST", "/agent/v1/recursor/heartbeat", rec.Token, hb, nil)

	var got api.Recursor
	e.call(200, "GET", "/api/v1/recursors/"+rec.RecursorID, "admin", nil, &got)
	if got.Status != "online" || !got.ConfigInSync || got.ListenIP != "10.0.0.43" || got.QPS != 10 || got.CacheHitRatio != 0.8 {
		t.Fatalf("recursor %+v", got)
	}
	if !got.AgentOutdated {
		t.Error("agent v1 vs panel v2 not outdated")
	}
	if len(got.UsedBy) != 1 || got.UsedBy[0].NodeName != "ns4" || !got.UsedBy[0].ECS {
		t.Fatalf("used_by %+v", got.UsedBy)
	}
	var ms api.RecursorMetricSeries
	e.call(200, "GET", "/api/v1/recursors/"+rec.RecursorID+"/metrics?from="+now.Add(-time.Hour).Format(time.RFC3339), "admin", nil, &ms)
	var q, sf, out int64
	for _, p := range ms.Points {
		q, sf, out = q+p.Queries, sf+p.Servfail, out+p.Outgoing
	}
	if q != 100 || sf != 2 || out != 30 {
		t.Fatalf("metrics queries=%d servfail=%d outgoing=%d", q, sf, out)
	}

	// A degraded resolver and its error.
	hb.Time, hb.ProbeOK, hb.ProbeError = now.Add(time.Second), false, "test lookup timed out"
	e.call(200, "POST", "/agent/v1/recursor/heartbeat", rec.Token, hb, nil)
	e.call(200, "GET", "/api/v1/recursors/"+rec.RecursorID, "admin", nil, &got)
	if got.Status != "degraded" || !strings.Contains(got.LastError, "timed out") {
		t.Fatalf("degraded: %s %q", got.Status, got.LastError)
	}

	// A new node changes the allow-list and the version; a deleted one drops out again.
	n2 := e.enrollNode("ns5", "198.51.100.10")
	var cfg2 api.RecursorConfig
	e.call(200, "GET", "/agent/v1/recursor/config", rec.Token, nil, &cfg2)
	if cfg2.Version == cfg.Version || !slices.Contains(cfg2.Allow, "198.51.100.10/32") {
		t.Fatalf("node join: version %d→%d allow %v", cfg.Version, cfg2.Version, cfg2.Allow)
	}
	e.call(204, "DELETE", "/api/v1/nodes/"+n2.NodeID, "admin", nil, nil)
	var cfg3 api.RecursorConfig
	e.call(200, "GET", "/agent/v1/recursor/config", rec.Token, nil, &cfg3)
	if cfg3.Version != cfg.Version || slices.Contains(cfg3.Allow, "198.51.100.10/32") {
		t.Fatalf("node delete: version %d allow %v", cfg3.Version, cfg3.Allow)
	}

	// Commands are delivered once and acked.
	var c api.Command
	e.call(202, "POST", "/api/v1/recursors/"+rec.RecursorID+"/commands", "admin", api.CommandRequest{Type: "restart_resolver"}, &c)
	e.call(422, "POST", "/api/v1/recursors/"+rec.RecursorID+"/commands", "admin", api.CommandRequest{Type: "restart_dnsdist"}, nil)
	hb.Time = now.Add(2 * time.Second)
	e.call(200, "POST", "/agent/v1/recursor/heartbeat", rec.Token, hb, &ack)
	if len(ack.Commands) != 1 || ack.Commands[0].ID != c.ID || ack.Commands[0].Type != "restart_resolver" {
		t.Fatalf("commands %+v", ack.Commands)
	}
	hb.Time, hb.AckedCommands = now.Add(3*time.Second), []int64{c.ID}
	var ack2 api.RecursorAck
	e.call(200, "POST", "/agent/v1/recursor/heartbeat", rec.Token, hb, &ack2)
	if len(ack2.Commands) != 0 {
		t.Fatalf("command delivered twice: %+v", ack2.Commands)
	}
	var acked bool
	if err := e.pool.QueryRow(context.Background(), "SELECT acked_at IS NOT NULL FROM recursor_commands WHERE id = $1", c.ID).
		Scan(&acked); err != nil || !acked {
		t.Fatalf("command not acked: %v %v", acked, err)
	}

	// Delete revokes the token.
	e.call(204, "DELETE", "/api/v1/recursors/"+rec.RecursorID, "admin", nil, nil)
	e.call(401, "GET", "/agent/v1/recursor/config", rec.Token, nil, nil)
	e.call(404, "GET", "/api/v1/recursors/"+rec.RecursorID, "admin", nil, nil)
}

func TestRecursorPatch(t *testing.T) {
	e := setup(t)
	rec := e.enrollRecursor("rec1", "unbound")
	var before api.RecursorConfig
	e.call(200, "GET", "/agent/v1/recursor/config", rec.Token, nil, &before)

	for _, bad := range []api.RecursorSettings{
		{AllowExtra: []string{"0.0.0.0/0"}},
		{AllowExtra: []string{"::/0"}},
		{AllowExtra: []string{"10.0.0.0/4"}},
		{AllowExtra: []string{"not-an-ip"}},
		{ECS: true, ECSZones: []string{"evil'\nzone"}},
		{CacheMB: 5},
	} {
		e.call(400, "PATCH", "/api/v1/recursors/"+rec.RecursorID, "admin", api.RecursorPatch{Settings: &bad}, nil)
	}
	bad := "not an ip"
	e.call(400, "PATCH", "/api/v1/recursors/"+rec.RecursorID, "admin", api.RecursorPatch{PublicIP: &bad}, nil)

	st := api.RecursorSettings{AllowExtra: []string{" 203.0.113.0/24 ", "203.0.113.0/24", "2001:db8::/48"}, ECS: true,
		ECSZones: []string{"GoogleVideo.com.", "akamaiedge.net"}, CacheMB: 256}
	var got api.Recursor
	e.call(200, "PATCH", "/api/v1/recursors/"+rec.RecursorID, "admin", api.RecursorPatch{Settings: &st}, &got)
	if !slices.Equal(got.Settings.AllowExtra, []string{"203.0.113.0/24", "2001:db8::/48"}) ||
		!slices.Equal(got.Settings.ECSZones, []string{"googlevideo.com", "akamaiedge.net"}) || got.Settings.CacheMB != 256 {
		t.Fatalf("settings %+v", got.Settings)
	}
	var after api.RecursorConfig
	e.call(200, "GET", "/agent/v1/recursor/config", rec.Token, nil, &after)
	if after.Version == before.Version || !slices.Contains(after.Allow, "203.0.113.0/24") ||
		!slices.Contains(after.Allow, "2001:db8::/48") || slices.Contains(after.Allow, "0.0.0.0/0") {
		t.Fatalf("config after patch %+v", after)
	}
	// A viewer-only request cannot change settings (no session → 401).
	e.call(401, "PATCH", "/api/v1/recursors/"+rec.RecursorID, "", api.RecursorPatch{Settings: &st}, nil)
}

func TestInstallRecursorScript(t *testing.T) {
	e := setup(t)
	resp, err := http.Get(e.srv.URL + "/install-recursor.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), "PANEL_URL='http://panel.test:8080'") {
		t.Fatalf("script %d:\n%.300s", resp.StatusCode, b)
	}
	if sh, err := exec.LookPath("sh"); err == nil {
		f := t.TempDir() + "/install-recursor.sh"
		os.WriteFile(f, b, 0o644)
		if out, err := exec.Command(sh, "-n", f).CombinedOutput(); err != nil {
			t.Fatalf("sh -n: %v\n%s", err, out)
		}
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
