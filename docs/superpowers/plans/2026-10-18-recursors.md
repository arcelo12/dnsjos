# Recursors Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Recursors menu that installs (one-line script), manages (allow-list, cache, ECS) and monitors PowerDNS Recursor / Unbound hosts that DnsJos nodes use as upstreams, plus a recursor picker in the profile upstreams editor.

**Architecture:** New panel package `internal/panel/recursors` with its own tables (migration 0015), its own enrollment tokens and bearer-token agent routes under `/agent/v1/recursor/*`. The same `dnsjos-agent` binary runs in recursor mode (`agent.json` `role: "recursor"`): it pulls a `RecursorConfig`, renders the resolver config with the pure package `internal/agent/resolver`, reloads/restarts the resolver (rollback on failure) and posts `RecursorHeartbeat`s. The SPA gets list/detail pages modelled on Nodes.

**Tech Stack:** Go 1.25, pgx v5, Postgres, miekg/dns (probe), React 19 + TanStack Query + recharts (web/).

**Spec:** `docs/superpowers/specs/2026-10-18-recursors-design.md`

## Global Constraints

- Allow-list = loopback (`127.0.0.0/8`, `::1/128`) + every live node's `public_ip` and heartbeat `public_ipv6` + manual `allow_extra`. Never `0.0.0.0/0`; manual entries with prefix < /8 (IPv4) or < /16 (IPv6) are rejected (panel 400) and dropped (agent).
- Kinds: `pdns` (PowerDNS Recursor, repo.powerdns.com `rec-54`) and `unbound` (distro). Fixed at enrollment.
- Fixed resolver defaults: DNSSEC `validate`, serve-stale, prefetch (as install.sh.tmpl section 4b).
- Cache auto sizing = installer formulas: pdns record cache `clamp(RAM_MB*250, 200000, 2000000)` entries, packet cache half; unbound rrset `clamp(RAM_MB/16, 64, 1024)` MB, msg half. Manual `cache_mb`: pdns entries = `cache_mb*1000`; unbound rrset = `cache_mb*2/5` MB, msg half.
- Listen: local IPv4 holding the default route + global IPv6 if present + `127.0.0.1` (probe), port 53. Never `0.0.0.0` (systemd-resolved owns 127.0.0.53/54).
- ECS scope-zero / NAT: scope_zero_address = recursor `public_ip` when it is a public IPv4 (never a private/CGNAT address).
- Migrations: plain SQL `migrations/0015_recursors.up.sql`. Handlers: pgx directly + `audit.Record`.
- No new dependencies.

## Review Focus

1. Manual allow entry `0.0.0.0/0`, `::/0`, `10.0.0.0/4` or garbage → PATCH 400; agent's `resolver.SafeAllow` drops them anyway (tests in Tasks 2 and 4).
2. Recursor behind NAT (enroll public IP ≠ listen IP) → listens on the local IP, scope-zero = public IP (Task 2 render test).
3. Resolver fails to start after a config change → previous files restored, resolver restarted, `apply_error` set, status degraded (Task 5 rollback test, NoSystemd fake runner).
4. A recursor token on node routes / a node token on recursor routes → 401 (Task 4 test).
5. Node deleted or enrolled → recursor config version changes and the allow-list follows (Task 4 test).

---

### Task 1: Shared API types + migration

**Files:**
- Create: `internal/shared/api/recursor.go`, `migrations/0015_recursors.up.sql`
- Modify: `internal/shared/api/agent.go` (SystemStats gets `CPUs int`, `CPUPct float64`, `DiskTotalMB int64`), `internal/agent/collect/collect.go` (fill them; CPU % from two /proc/stat samples kept in a package-level sampler)
- Test: `internal/shared/api/recursor_test.go`

**Interfaces — Produces:**
- `RecursorPDNS = "pdns"`, `RecursorUnbound = "unbound"`, `RecursorKinds = []string{...}`
- `RecursorSettings{AllowExtra []string; ECS bool; ECSZones []string; CacheMB int}` with `Validate() error` (allow entries IP or CIDR, min prefix /8 v4 /16 v6, ≤ 256 entries; zones hostnames ≤ 100; CacheMB 0 or 32..65536)
- `Recursor`, `RecursorUser{NodeID, NodeName, Address string; ECS bool}`, `RecursorPatch{Name, PublicIP *string; Settings *RecursorSettings}`
- `RecursorTokenCreate{Name, Kind string; TTLHours int}`, `RecursorTokenCreated{ID, Token, InstallCommand string; ExpiresAt time.Time}`, `RecursorToken`
- `RecursorEnrollRequest{Token, Hostname, Kind, OS, Arch, AgentVersion, ResolverVersion, PublicIP string}`, `RecursorEnrollResponse{RecursorID, Name, Kind, Token string; PollIntervalS int}`
- `RecursorConfig{Version int; Kind, PublicIP string; Allow []string; Settings RecursorSettings; PollIntervalS, HeartbeatIntervalS int}`
- `RecursorCounters{Queries, CacheHits, CacheMisses, Servfail, Outgoing int64}`
- `RecursorHeartbeat{Time; AgentVersion, ResolverVersion, OS string; UptimeS int64; ResolverRunning, ProbeOK bool; ProbeError string; ProbeMs float64; AppliedConfigVersion int; ApplyError string; ListenIP, ListenIPv6 string; System SystemStats; Counters RecursorCounters; LatencyAvgMs float64; AckedCommands []int64; LastUpgrade *UpgradeResult}`
- `RecursorAck{ConfigVersion int; Commands []Command}`, `RecursorLive{RecursorID string; Heartbeat *RecursorHeartbeat; ReceivedAt *time.Time}`
- `RecursorMetricSample{TS; Queries, CacheHits, CacheMisses, Servfail, Outgoing int64; LatencyAvgMs, QPS, CacheHitRatio, ServfailRatio, OutgoingQPS float64}`, `RecursorMetricSeries{From, To; StepS int; Points []RecursorMetricSample}`
- `CmdRestartResolver = "restart_resolver"`; recursor commands = reapply, restart_resolver, upgrade_agent
- Tables: `recursors`, `recursor_tokens`, `recursor_commands`, `recursor_metrics_minutely`

- [ ] Step 1: test `TestRecursorSettingsValidate`: accepts `{"203.0.113.7", "198.51.100.0/24", "2001:db8::/48"}`; rejects `"0.0.0.0/0"`, `"::/0"`, `"10.0.0.0/4"`, `"nope"`, 257 entries, `CacheMB: 10`.
- [ ] Step 2: implement types + Validate; migration with CHECKs (kind, status pending/online/degraded/offline, command type) and partial unique index on live names.
- [ ] Step 3: `go test ./internal/shared/api/ ./internal/agent/collect/` pass; commit.

### Task 2: Resolver rendering + stats parsing (`internal/agent/resolver`)

**Files:** Create `internal/agent/resolver/render.go`, `stats.go`, `render_test.go`, `stats_test.go`

**Interfaces — Produces:**
- `type Facts struct{ ListenIPs []string; Threads, MemMB int; IPv6 bool; UnboundControl bool }`
- `type File struct{ Path, Content string }` (Path absolute, e.g. `/etc/powerdns/recursor.d/dnsjos.yml`)
- `func Render(c api.RecursorConfig, f Facts) (main File, acl File, err error)` — pdns: `/etc/powerdns/recursor.d/dnsjos.yml` (+ `incoming.allow_from_file`) and `/etc/powerdns/dnsjos-allow.txt`; unbound: `/etc/unbound/unbound.conf.d/dnsjos.conf` and `/etc/unbound/unbound.conf.d/dnsjos-acl.conf`
- `func SafeAllow(in []string) []string` — loopback always, invalid/short prefixes dropped, sorted, deduped, hosts → /32 or /128
- `func ServiceName(kind string) string` (`pdns-recursor` | `unbound`)
- `func ParsePDNS(out string) (api.RecursorCounters, float64)` from `rec_control get-all` (`questions`, `packetcache-hits`, `packetcache-misses`, `servfail-answers`, `all-outqueries`, `qa-latency` µs→ms)
- `func ParseUnbound(out string) (api.RecursorCounters, float64)` from `unbound-control stats_noreset` (`total.num.queries`, `total.num.cachehits`, `total.num.cachemiss`, `num.answer.rcode.SERVFAIL`, `num.query.udpout`+`num.query.tcpout`, `total.recursion.time.avg` s→ms)

- [ ] Step 1: tests: pdns render with ECS zones `["googlevideo.com"]` contains `use_incoming_edns_subnet: true`, `- 'googlevideo.com'`, `scope_zero_address: 192.0.2.53`, listens `10.0.0.43:53` and `127.0.0.1:53`, never `0.0.0.0:53`; private public_ip `10.1.2.3` → no scope_zero; ECS off → no ecs keys; CacheMB 0 with MemMB 4096 → `max_entries: 1024000`; unbound render has `access-control: 198.51.100.99/32 allow`, `module-config: "subnetcache validator iterator"` only with ECS; `SafeAllow([]string{"0.0.0.0/0","1.2.3.4","bad"})` == `["1.2.3.4/32","127.0.0.0/8","::1/128"]`; stats parse fixtures.
- [ ] Step 2: implement; Step 3: `go test ./internal/agent/resolver/` pass; commit.

### Task 3: Agent recursor mode

**Files:** Create `internal/agent/recursor.go`, `internal/agent/recursor_test.go`; Modify `internal/agent/agent.go` (Config gets `Role, Kind string`; `EnrollRecursor`), `internal/agent/client/client.go` (`EnrollRecursor`, `RecursorConfig`, `RecursorHeartbeat`), `internal/agent/upgrade.go` (extract `replaceAgentBinary(ctx, cl, version) error` and `confirmAgentUpgrade(o) *api.UpgradeResult` used by both modes), `cmd/dnsjos-agent/main.go` (`enroll --recursor --kind K`; `run` dispatches on Config.Role)

**Interfaces — Consumes:** Task 1 types, Task 2 `resolver.*`. **Produces:** `agent.RunRecursor(ctx, Options) error`, `agent.EnrollRecursor(ctx, o, panelURL, token, kind, name string) error`.

Behaviour: config loop (ETag on version) → `resolver.Render` → write changed files (backup old) → pdns: only ACL changed → `rec_control reload-acls`, else `systemctl restart pdns-recursor`; unbound: `unbound-checkconf` then `unbound-control reload_keep_cache` (fallback `reload`, then restart). After restart: `systemctl is-active` within 10 s; failure → restore backups, restart, `applyErr`. Heartbeat loop: probe `a.root-servers.net. A` at `127.0.0.1:53` (2 s), stats via rec_control/unbound-control, versions, facts, `collect.System`, commands (reapply, restart_resolver, upgrade_agent). NoSystemd: no systemctl/control commands (runner stub in tests).

- [ ] Step 1: test `TestRecursorApplyRollback` with a fake runner whose `systemctl is-active` fails after restart: old file contents restored, applyErr non-empty; `TestRecursorApplyACLOnly` → only `rec_control reload-acls` ran.
- [ ] Step 2: implement; Step 3: `go test ./internal/agent/...` pass; commit.

### Task 4: Panel recursors package

**Files:** Create `internal/panel/recursors/{register.go,tokens.go,agent.go,recursors.go,allow.go,install.go,recursors_test.go}`, `internal/panel/install/install-recursor.sh.tmpl`; Modify `internal/panel/install/install.go` (embed new template), `internal/panel/app/router.go` (`Recursor(pattern, h)` + `RecursorIDFrom`), `internal/panel/server/server.go` (register), `internal/panel/reports/{register.go,metrics.go,retention.go}` (recursor metrics route + retention), `internal/panel/nodes/nodes.go` (export `ServedUpstreams(ctx, q) ([]NodeUpstream, error)` — `NodeUpstream{NodeID, NodeName string; Servers []api.Upstream}` from each live node's effective config)

Routes: Viewer `GET /api/v1/recursors`, `/{id}`, `/{id}/live`, `/{id}/metrics`, `GET /api/v1/recursor-tokens`; Admin `POST /api/v1/recursor-tokens`, `DELETE /api/v1/recursor-tokens/{id}`, `PATCH|DELETE /api/v1/recursors/{id}`, `POST /api/v1/recursors/{id}/commands`; Public `POST /agent/v1/recursor/enroll`, `GET /install-recursor.sh`; Recursor `GET /agent/v1/recursor/config`, `POST /agent/v1/recursor/heartbeat`. Job `recursor-status` every 15 s (offline after 3 heartbeat intervals). Install command: `curl -fsSL <url>/install-recursor.sh | sudo sh -s -- --token T --kind K`.

Config version = `fnv32a(json(kind, public_ip, allow, settings)) % 2147483646 + 1`.

- [ ] Step 1: tests (dbtest `recursors`): token → enroll → config (allow has loopback + node IP `198.51.100.10/32`) → heartbeat → status online, metrics row; node deleted → version changes and IP gone; PATCH with `allow_extra: ["0.0.0.0/0"]` → 400; recursor token on `/agent/v1/config` → 401 and node token on `/agent/v1/recursor/config` → 401; used_by lists a node whose upstream is `<public_ip>:53`.
- [ ] Step 2: implement; Step 3: `DNSJOS_TEST_DATABASE_URL=... go test ./internal/panel/...` pass; `sh -n` on rendered installer; commit.

### Task 5: Web UI

**Files:** Create `web/src/pages/recursors/{RecursorsPage,RecursorDetailPage}.tsx`, `web/src/components/recursors/{AddRecursorDialog,RecursorsTable,RecursorSettingsTab,RecursorActionsTab,RecursorOverviewTab,RecursorPicker}.tsx`; Modify `web/src/lib/api/{types,client}.ts`, `web/src/app/{layout,routes}.tsx`, `web/src/components/nodes/MetricChart.tsx` (generic row type), `web/src/components/profiles/UpstreamsEditor.tsx` (RecursorPicker next to "Add upstream": appends `{name, address: public_ip+':53', weight: 10, order: 1, sockets: 4, ecs: settings.ecs}`)

Overview tab: charts QPS, cache hit %, latency, SERVFAIL %, outgoing qps; Host card (CPU %, load, memory, disk used/total, uptime, OS); Network card (public IPv4, listen IPv4, IPv6); Used-by table with ECS-mismatch badge. Settings tab: allow-extra textarea, ECS switch + zones, cache MB (0 = auto), read-only automatic allow-list preview from `/live`… (served via `Recursor.allow`).

- [ ] Step 1: `pnpm -C web typecheck && pnpm -C web lint && pnpm -C web build` pass; commit.

### Task 6: Docs, full verification, deploy

- [ ] SPEC.md new section "§20 Recursors", OPERATIONS.md "Adding a recursor".
- [ ] `go vet ./... && go test ./...` (with test DB), web checks, `make release`.
- [ ] Deploy panel (pg_dump, keep .prev), reinstall 192.0.2.53 via the new script, verify it shows online with metrics, dig from ns4 through it with ECS, outsider refused.
