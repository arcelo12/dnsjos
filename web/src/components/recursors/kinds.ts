import type { Recursor, RecursorKind, RecursorMetricSeries } from '@/lib/api/types'

export const KIND_INFO: Record<RecursorKind, { label: string; help: string }> = {
  pdns: {
    label: 'PowerDNS Recursor',
    help: 'Same vendor and repo as dnsdist (repo.powerdns.com), fine-grained ECS. The usual choice.',
  },
  unbound: { label: 'Unbound', help: 'Light and simple; the distro package.' },
}

export const kindLabel = (k: string) => KIND_INFO[k as RecursorKind]?.label ?? k

export interface RecursorRow {
  ts: number
  qps: number
  hit: number // cache hit %, 0..100
  latency: number
  servfail: number // SERVFAIL share %, 0..100
  outgoing: number // queries to authoritative servers per second
}

const round = (n: number, d: number) => Math.round(n * 10 ** d) / 10 ** d

/** Maps server samples (rates are derived server-side) to chart rows. */
export function toRecursorRows(m: RecursorMetricSeries | undefined): RecursorRow[] {
  if (!m) return []
  return m.points.map((p) => ({
    ts: new Date(p.ts).getTime(),
    qps: round(p.qps, 1),
    hit: round(p.cache_hit_ratio * 100, 1),
    latency: round(p.latency_avg_ms, 2),
    servfail: round(p.servfail_ratio * 100, 2),
    outgoing: round(p.outgoing_qps, 1),
  }))
}

/** Distinct nodes with this recursor among their upstreams. */
export const nodesUsing = (r: Recursor) => new Set(r.used_by.map((u) => u.node_id)).size
