import { LuDatabase, LuTrash2 } from 'react-icons/lu'
import { Link, useNavigate } from 'react-router'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/ConfirmDialog'
import { DataTable, type Column } from '@/components/DataTable'
import { EmptyState } from '@/components/EmptyState'
import { toastError } from '@/components/ops/toast'
import { StatusBadge } from '@/components/StatusBadge'
import { TimeAgo } from '@/components/TimeAgo'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useDeleteRecursorToken, useRecursorTokens } from '@/lib/api/client'
import type { Recursor, RecursorToken } from '@/lib/api/types'
import { pendingTokens } from '@/lib/enrollment'
import { fmtMs, fmtPercent, fmtQps } from '@/lib/format'

import { kindLabel, nodesUsing } from './kinds'

const columns: Column<Recursor>[] = [
  {
    key: 'name',
    header: 'Recursor',
    sortValue: (r) => r.name,
    cell: (r) => (
      <div className="min-w-0">
        <Link to={`/recursors/${r.id}`} className="font-medium hover:underline" onClick={(e) => e.stopPropagation()}>
          {r.name}
        </Link>
        <div className="truncate font-mono text-xs text-muted-foreground">{r.public_ip || r.hostname || '—'}</div>
      </div>
    ),
  },
  { key: 'status', header: 'Status', sortValue: (r) => r.status, cell: (r) => <StatusBadge status={r.status} /> },
  {
    key: 'kind',
    header: 'Resolver',
    sortValue: (r) => r.kind,
    cell: (r) => (
      <div className="text-xs leading-tight">
        <div>{kindLabel(r.kind)}</div>
        <div className="text-muted-foreground">{r.resolver_version || '—'}</div>
      </div>
    ),
  },
  { key: 'qps', header: 'QPS', align: 'right', sortValue: (r) => r.qps, cell: (r) => fmtQps(r.qps) },
  { key: 'hit', header: 'Cache hit', align: 'right', sortValue: (r) => r.cache_hit_ratio, cell: (r) => fmtPercent(r.cache_hit_ratio) },
  { key: 'lat', header: 'Latency', align: 'right', sortValue: (r) => r.latency_avg_ms, cell: (r) => fmtMs(r.latency_avg_ms) },
  {
    key: 'nodes',
    header: 'Used by',
    align: 'right',
    sortValue: (r) => nodesUsing(r),
    cell: (r) => (
      <span className="tabular">
        {nodesUsing(r)} node{nodesUsing(r) === 1 ? '' : 's'}
      </span>
    ),
  },
  {
    key: 'ecs',
    header: 'ECS',
    sortValue: (r) => Number(r.settings.ecs),
    cell: (r) =>
      r.settings.ecs ? (
        <Badge variant="outline" className="border-success/30 font-normal text-success">
          on
        </Badge>
      ) : (
        <span className="text-xs text-muted-foreground">off</span>
      ),
  },
  { key: 'seen', header: 'Last seen', sortValue: (r) => r.last_seen_at, cell: (r) => <TimeAgo date={r.last_seen_at} className="text-muted-foreground" /> },
]

export function RecursorsTable({ rows, loading, error }: { rows: Recursor[] | undefined; loading?: boolean; error?: Error | null }) {
  const nav = useNavigate()
  return (
    <DataTable
      columns={columns}
      rows={rows}
      rowKey={(r) => r.id}
      loading={loading}
      error={error}
      defaultSort={{ key: 'name' }}
      onRowClick={(r) => nav(`/recursors/${r.id}`)}
      empty={
        <EmptyState
          icon={LuDatabase}
          title="No recursors yet"
          description="Add a PowerDNS Recursor or Unbound host and pick it as an upstream in your profiles."
        />
      }
    />
  )
}

/** Unused, unexpired recursor tokens with a Revoke button; hidden when there are none. */
export function PendingRecursorTokens() {
  const tokens = useRecursorTokens()
  const remove = useDeleteRecursorToken()
  const pending = pendingTokens(tokens.data?.items ?? [])
  if (!pending.length) return null
  const cols: Column<RecursorToken>[] = [
    { key: 'name', header: 'Name', cell: (t) => t.name || <span className="text-muted-foreground italic">hostname</span> },
    { key: 'kind', header: 'Resolver', cell: (t) => kindLabel(t.kind) },
    { key: 'created', header: 'Created', sortValue: (t) => t.created_at, cell: (t) => <TimeAgo date={t.created_at} /> },
    { key: 'expires', header: 'Expires', sortValue: (t) => t.expires_at, cell: (t) => <TimeAgo date={t.expires_at} /> },
    {
      key: 'actions',
      header: '',
      align: 'right',
      cell: (t) => (
        <ConfirmDialog
          title="Revoke recursor token?"
          description="Its install command stops working immediately."
          confirmLabel="Revoke"
          destructive
          onConfirm={() => remove.mutateAsync(t.id).then(() => toast.success('Token revoked'), toastError)}
        >
          <Button variant="ghost" size="sm" className="text-destructive">
            <LuTrash2 /> Revoke
          </Button>
        </ConfirmDialog>
      ),
    },
  ]
  return (
    <Card className="mt-4">
      <CardHeader>
        <CardTitle>Pending recursors</CardTitle>
        <CardDescription>Install commands that have not been used yet.</CardDescription>
      </CardHeader>
      <CardContent className="px-0">
        <DataTable columns={cols} rows={pending} rowKey={(t) => t.id} defaultSort={{ key: 'created', desc: true }} />
      </CardContent>
    </Card>
  )
}
