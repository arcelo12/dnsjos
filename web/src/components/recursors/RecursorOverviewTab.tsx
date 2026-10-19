import { useState } from 'react'
import { LuNetwork } from 'react-icons/lu'
import { Link } from 'react-router'

import { DataTable, type Column } from '@/components/DataTable'
import { EmptyState } from '@/components/EmptyState'
import { MetricChart } from '@/components/nodes/MetricChart'
import { Fact } from '@/components/nodes/NodeOverviewTab'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { RANGES, useRecursorRangeMetrics, type Range } from '@/lib/api/nodes'
import type { Recursor, RecursorLive, RecursorUser } from '@/lib/api/types'
import { fmtDuration, fmtMs, fmtNumber } from '@/lib/format'

import { toRecursorRows, type RecursorRow } from './kinds'

const pctOf = (used: number, total: number) => (total > 0 ? ` (${Math.round((used / total) * 100)}%)` : '')

export function RecursorOverviewTab({ rec, live }: { rec: Recursor; live: RecursorLive | undefined }) {
  const [range, setRange] = useState<Range>('6h')
  const metrics = useRecursorRangeMetrics(rec.id, range)
  const chart = { rows: toRecursorRows(metrics.data), loading: metrics.isPending, spanS: RANGES[range] }
  const hb = live?.heartbeat
  const sys = hb?.system
  const usedCols: Column<RecursorUser>[] = [
    {
      key: 'node',
      header: 'Node',
      sortValue: (u) => u.node_name,
      cell: (u) => (
        <Link to={`/nodes/${u.node_id}`} className="font-medium hover:underline">
          {u.node_name}
        </Link>
      ),
    },
    { key: 'addr', header: 'Upstream', cell: (u) => <span className="font-mono text-xs">{u.address}</span> },
    {
      key: 'ecs',
      header: 'ECS',
      cell: (u) =>
        u.ecs === rec.settings.ecs ? (
          <span className="text-xs text-muted-foreground">{u.ecs ? 'on' : 'off'}</span>
        ) : (
          <Badge variant="outline" className="border-warning/30 bg-warning/10 font-normal text-warning">
            {u.ecs ? 'on, but the recursor has ECS off' : 'off: turn ECS on for this upstream'}
          </Badge>
        ),
    },
  ]

  return (
    <div className="space-y-4">
      <Tabs value={range} onValueChange={(v) => setRange(v as Range)} className="gap-4">
        <div className="flex items-center justify-between gap-2">
          <h2 className="text-sm font-medium">Traffic</h2>
          <TabsList aria-label="Time range">
            {Object.keys(RANGES).map((r) => (
              <TabsTrigger key={r} value={r} className="px-3 text-xs">
                {r}
              </TabsTrigger>
            ))}
          </TabsList>
        </div>
        <TabsContent value={range} className="grid grid-cols-1 gap-3 lg:grid-cols-2">
          <MetricChart<RecursorRow> title="Queries per second" {...chart} series={[{ key: 'qps', label: 'QPS', color: 'var(--chart-1)' }]} />
          <MetricChart<RecursorRow>
            title="Cache hit ratio"
            description={rec.kind === 'pdns' ? 'Answers from the packet cache.' : undefined}
            kind="line"
            unit="%"
            domain={[0, 100]}
            {...chart}
            series={[{ key: 'hit', label: 'Cache hit %', color: 'var(--chart-2)' }]}
          />
          <MetricChart<RecursorRow>
            title={rec.kind === 'pdns' ? 'Average answer time' : 'Average recursion time'}
            description={rec.kind === 'unbound' ? 'Cache misses only.' : undefined}
            kind="line"
            unit=" ms"
            {...chart}
            series={[{ key: 'latency', label: 'Latency ms', color: 'var(--chart-3)' }]}
          />
          <MetricChart<RecursorRow>
            title="SERVFAIL rate"
            description="A rise usually means an unreachable authoritative server or a DNSSEC failure."
            kind="line"
            unit="%"
            {...chart}
            series={[{ key: 'servfail', label: 'SERVFAIL %', color: 'var(--chart-5)' }]}
          />
          <MetricChart<RecursorRow>
            title="Queries to authoritative servers"
            {...chart}
            series={[{ key: 'outgoing', label: 'Outgoing/s', color: 'var(--chart-4)' }]}
          />
        </TabsContent>
      </Tabs>

      <div className="grid gap-3 lg:grid-cols-2">
        <Card className="gap-2 py-4">
          <CardHeader className="px-4">
            <CardTitle className="text-sm">Network</CardTitle>
          </CardHeader>
          <CardContent className="grid grid-cols-2 gap-3 px-4 text-sm">
            <Fact label="Public IP (nodes use this)" value={<span className="font-mono">{rec.public_ip || '—'}</span>} />
            <Fact label="Listens on" value={<span className="font-mono">{rec.listen_ip ? `${rec.listen_ip}:53` : '—'}</span>} />
            <Fact label="IPv6" value={<span className="font-mono">{rec.listen_ipv6 || 'none'}</span>} />
            <Fact label="Test lookup" value={hb ? (hb.probe_ok ? fmtMs(hb.probe_ms) : 'failing') : '—'} />
          </CardContent>
        </Card>
        <Card className="gap-2 py-4">
          <CardHeader className="px-4">
            <CardTitle className="text-sm">Host</CardTitle>
          </CardHeader>
          <CardContent className="grid grid-cols-2 gap-3 px-4 text-sm sm:grid-cols-3">
            <Fact label="CPU" value={sys ? `${sys.cpu_pct.toFixed(1)}% of ${sys.cpus || '—'} cores` : '—'} />
            <Fact label="Load (1 min)" value={sys ? sys.load1.toFixed(2) : '—'} />
            <Fact
              label="Memory used"
              value={
                sys
                  ? `${fmtNumber(sys.mem_total_mb - sys.mem_avail_mb)} / ${fmtNumber(sys.mem_total_mb)} MB${pctOf(sys.mem_total_mb - sys.mem_avail_mb, sys.mem_total_mb)}`
                  : '—'
              }
            />
            <Fact
              label="Disk used"
              value={
                sys && sys.disk_total_mb
                  ? `${fmtNumber(sys.disk_total_mb - sys.disk_free_mb)} / ${fmtNumber(sys.disk_total_mb)} MB${pctOf(sys.disk_total_mb - sys.disk_free_mb, sys.disk_total_mb)}`
                  : sys
                    ? `${fmtNumber(sys.disk_free_mb)} MB free`
                    : '—'
              }
            />
            <Fact label="Agent uptime" value={hb ? fmtDuration(hb.uptime_s) : '—'} />
            <Fact label="OS" value={rec.os || '—'} />
          </CardContent>
        </Card>
      </div>

      <h2 className="text-sm font-medium">Used by</h2>
      <DataTable
        columns={usedCols}
        rows={rec.used_by}
        rowKey={(u) => u.node_id + u.address}
        defaultSort={{ key: 'node' }}
        empty={
          <EmptyState
            icon={LuNetwork}
            title="No node uses this recursor yet"
            description={`In a profile's upstreams, use "Add recursor" to point nodes at ${rec.public_ip || 'it'}.`}
          />
        }
      />
    </div>
  )
}
