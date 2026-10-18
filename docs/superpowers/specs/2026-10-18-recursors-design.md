# Recursors: panel-managed upstream resolvers

Status: draft for review · 2026-10-18

## Why

DnsJos nodes forward to upstream resolvers that are just addresses typed into a profile.
Running a dedicated recursor (PowerDNS Recursor or Unbound, with ECS) for the nodes today
means SSHing in, hand-writing the config, hand-maintaining an allow-list of node IPs, and
then typing the address into each profile and remembering to tick ECS. Nothing watches it.
(192.0.2.53 was set up exactly this way on 2026-10-18.)

The user wants adding a recursor to feel like adding a node: one install command from the
panel, an agent on the box, health and metrics in the panel, settings managed from the
panel, and an easy pick from the node side.

## Goals

1. A new **Recursors** menu (nav group next to Nodes) listing every recursor.
2. **Add recursor** gives a one-line install command (same shape as Add node:
   `curl -fsSL <panel>/install-recursor.sh | sudo sh -s -- --token T`), with a choice of
   **PowerDNS Recursor** or **Unbound** made in the dialog. The script installs the
   resolver, installs the agent in recursor mode, enrolls it.
3. **Panel-managed settings**, applied by the agent (pull, render, reload):
   - **Allow-list — automatic.** Every live (not deleted) node of the panel gets its IPs
     allowed (its `public_ip`, plus the public IPv6 it reports in heartbeats); a deleted
     node's IPs are removed. Plus a manual list of extra IPs/CIDRs (e.g. a monitoring
     host, a node reaching it over a private network). Loopback always allowed. Never an
     open resolver: an empty list means only loopback, and the manual list rejects
     public prefixes shorter than /16 (IPv4) or /32 (IPv6); private ranges may be added whole
     (tightened after review: a public /8 is effectively open).
     *Claude's call (user asleep):* "nodes that use it" is shown on the page, but the
     allow-list covers all fleet nodes. Deriving it from profile usage races with
     publishes and staged rollouts (a node starts querying before the recursor allowed
     it → REFUSED answers to clients); allowing the whole fleet has no such window and
     still admits only the operator's own servers.
   - **Cache size:** automatic from RAM (the installer's existing formula: ~250 record
     entries per MB, 200k..2M, packet cache half) with an optional manual override.
   - **ECS:** on/off, and optionally limited to a list of zones (as `--ecs-zones`).
   - Fixed defaults, not exposed: DNSSEC `validate`, serve-stale, prefetch (as the
     installer's section 4b already writes).
4. **Monitoring** on a recursor detail page and in the list:
   - Status: **online / degraded / offline**. Offline = no heartbeat (same rule as nodes).
     Degraded = resolver service not running, or a local test lookup fails, or the last
     settings apply failed.
   - Charts over time: QPS, cache hit rate, average answer latency, SERVFAIL rate,
     outgoing queries to authoritative servers.
   - Host: CPU (load, and CPU % if cheap), memory used/total, disk used/total.
   - IP information: public IPv4, listen/private IPv4 (differs behind NAT), IPv6 if any.
   - Which nodes use it (derived from configs) and, where cheap, their traffic share.
   - List columns: name, status, type (PowerDNS/Unbound), resolver version, public IP,
     QPS, number of nodes using it.
5. **Node-side picker.** In the profile upstreams editor (and node overrides), an option
   "Add recursor" that picks from the Recursors list and fills the upstream address
   (`<public IP>:53`) and sets ECS on when the recursor has ECS on.
6. Operational parity with nodes where it is the same code: restart, reapply, agent
   upgrade, delete.

## Non-goals

- Adopting an already hand-installed recursor. 192.0.2.53 will simply be re-installed
  with the new script over its current setup.
- Switching a recursor between PowerDNS and Unbound after install (delete and re-add).
- Resolver upgrades managed by panel rollouts (apt keeps them current; can come later).
- Prometheus/Grafana integration for recursors.
- Running a recursor on a node host as a panel-managed recursor (the existing
  `--with-recursor` loopback option on nodes stays as it is).
- Anycast, clustering, or load-balancing logic beyond what dnsdist upstream policies do.

## Anti-goals

- Any state in which the recursor answers queries from IPs that are not loopback, a node
  that uses it, or the manual list. A config render bug must fail closed (keep the last
  good config), never fall back to `0.0.0.0/0`.
- Leaking private/CGNAT addresses as ECS (keep the installer's NAT handling: the scope-0
  address is the public IP, never the private source).
- Requiring the user to SSH in after install for normal operations.

## Constraints

- Reuse the node machinery rather than build a parallel one: enrollment tokens and
  `/agent/v1/enroll`, bearer-token auth middleware, the pull-config / heartbeat / command
  ack loop, `LiveStore`, a minutely metrics table shaped like `metrics_minutely`, the
  existing install template sections for resolver install (4b) and agent install (5–7).
- One agent binary; recursor mode chosen at enroll and stored in `/etc/dnsjos/agent.json`.
  The agent in recursor mode does not touch dnsdist.
- Recursor hosts are often behind NAT (192.0.2.53: eth0 10.0.0.43, public
  192.0.2.53). The resolver listens on the local address that holds the default route;
  the panel learns both the local and the public address (public from the enroll request's
  source address, as nodes do today).
- Port 53 on the box may be held by systemd-resolved on 127.0.0.53/54: listen on the
  specific local address, not `0.0.0.0` (what was done by hand on 192.0.2.53).
- Debian 12/13 (amd64/arm64), same as nodes. PowerDNS Recursor from repo.powerdns.com
  (`rec-54` series, as the installer), Unbound from the distro.
- Metrics sources: PowerDNS Recursor `rec_control get-all` (or its web API);
  Unbound `unbound-control stats_noreset` (the installer already enables extended stats
  and remote-control). Counters are raw and monotonic; the panel computes deltas, as for
  dnsdist.
- Allow-list changes when a profile is published must reach the recursor without the user
  touching the recursor (the recursor's config version is bumped whenever its derived
  allow-list changes).
- Migrations are plain SQL in `migrations/NNNN_*.up.sql` (next: 0015). Handlers use pgx
  directly with `audit.Record`, like `internal/panel/nodes`.
- Frontend: React SPA in `web/`, nav in `web/src/app/layout.tsx`, routes in
  `web/src/app/routes.tsx`; follow `pages/nodes` and `components/nodes` patterns.
- Update `docs/SPEC.md` (new section) and `docs/OPERATIONS.md` (how to add a recursor).

## Decided by the user

- Separate menu "Recursors" with an install-script flow like nodes.
- Both PowerDNS Recursor and Unbound supported.
- An agent on the recursor for monitoring.
- The panel manages settings: allow-list, cache size, ECS.
- Node IPs are allowed automatically when a node uses the recursor.
- Monitoring shows health, resolver metrics, IP information, CPU, memory, storage.

## My calls (Claude), open to change

- Allow-list = all live nodes + manual list (see goal 3). "Used by" = nodes whose served
  effective config has an upstream whose IP equals the recursor's public or listen IP
  (upstream addresses are literal IP:port in the spec, so no name resolution is needed).
- Profile entries are copies: the detail page lists the nodes/profiles using the recursor
  and flags those whose upstream ECS flag differs from the recursor's ECS setting.
- Reinstalling 192.0.2.53: the agent applies the panel config seconds after enroll;
  the only interruption is the resolver restart.
- 192.0.2.53 is re-installed, not adopted.
- Cache size auto by default; DNSSEC/serve-stale/prefetch fixed, not exposed.
- Degraded includes a local test lookup (e.g. resolve a well-known name every heartbeat).
- Upstream picker fills `<public IP>:53` and mirrors the recursor's ECS setting.
- The recursor agent reuses the existing host-stats collector, extended with disk total
  and CPU % (today it reports load1, mem total/avail, disk free only), which nodes get too.

## Left to the builder

- Whether recursors are a `kind` on `nodes` or their own `recursors` table (own table is
  the expectation given the different columns, but sharing enrollment tokens with a
  `kind` column is fine).
- Exact endpoint names, heartbeat/config payload shapes, metrics table shape.
- How the agent reloads each resolver (`rec_control reload-acls` / restart;
  `unbound-control reload`) and how it keeps the last good config on a failed apply.
- Chart layout and component reuse on the detail page.
- Tests, following the repo's existing patterns.
