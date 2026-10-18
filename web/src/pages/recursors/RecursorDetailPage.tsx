import { LuArrowLeft, LuCircleAlert, LuDatabase } from 'react-icons/lu'
import { Link, useParams } from 'react-router'

import { useAuth } from '@/app/auth'
import { EmptyState } from '@/components/EmptyState'
import { PageHeader } from '@/components/PageHeader'
import { SyncBadge } from '@/components/nodes/NodesTable'
import { RecursorActionsTab } from '@/components/recursors/RecursorActionsTab'
import { RecursorOverviewTab } from '@/components/recursors/RecursorOverviewTab'
import { RecursorSettingsTab } from '@/components/recursors/RecursorSettingsTab'
import { kindLabel, nodesUsing } from '@/components/recursors/kinds'
import { StatCard } from '@/components/StatCard'
import { StatusBadge } from '@/components/StatusBadge'
import { TimeAgo } from '@/components/TimeAgo'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ApiError, useRecursor, useRecursorLive } from '@/lib/api/client'
import { fmtMs, fmtPercent, fmtQps } from '@/lib/format'

export default function RecursorDetailPage() {
  const { id = '' } = useParams()
  const { isAdmin } = useAuth()
  const q = useRecursor(id)
  const live = useRecursorLive(id)
  const rec = q.data

  if (q.isError)
    return (
      <EmptyState
        icon={LuDatabase}
        title={q.error instanceof ApiError && q.error.status === 404 ? 'Recursor not found' : 'Could not load recursor'}
        description={q.error.message}
        action={
          <Button asChild variant="outline">
            <Link to="/recursors">Back to recursors</Link>
          </Button>
        }
      />
    )
  if (!rec)
    return (
      <div className="space-y-4">
        <Skeleton className="h-12 w-72" />
        <Skeleton className="h-24" />
        <Skeleton className="h-72" />
      </div>
    )

  const errors = rec.status === 'offline' ? [] : rec.last_error.split('; ').filter(Boolean)
  const n = nodesUsing(rec)

  return (
    <>
      <PageHeader
        identifier
        title={rec.name}
        description={
          <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <StatusBadge status={rec.status} />
            <span className="font-mono">{rec.public_ip || '—'}</span>
            {rec.hostname && <span>{rec.hostname}</span>}
            <span>{kindLabel(rec.kind)}</span>
            <span>
              seen <TimeAgo date={rec.last_seen_at} />
            </span>
          </span>
        }
        actions={
          <Button asChild variant="ghost" size="sm">
            <Link to="/recursors">
              <LuArrowLeft /> Recursors
            </Link>
          </Button>
        }
      />

      {errors.length > 0 && (
        <div className="flex gap-2 rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          <LuCircleAlert className="mt-0.5 size-4 shrink-0" />
          <ul className="space-y-0.5">
            {errors.map((e) => (
              <li key={e} className="font-mono text-xs break-all">
                {e}
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard title="QPS" value={fmtQps(rec.qps)} />
        <StatCard title="Cache hit" value={fmtPercent(rec.cache_hit_ratio)} />
        <StatCard title="Avg latency" value={fmtMs(rec.latency_avg_ms)} />
        <StatCard
          title="Versions"
          value={<span className="text-base">{rec.resolver_version || '—'}</span>}
          hint={`agent ${rec.agent_version || '—'}${rec.agent_outdated ? ' (outdated)' : ''}`}
        />
      </div>
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <SyncBadge ok={rec.config_in_sync} label="config" />
        <span>
          {rec.allow.length} allowed networks · ECS {rec.settings.ecs ? 'on' : 'off'} · used by {n} node{n === 1 ? '' : 's'}
        </span>
      </div>

      <Tabs defaultValue="overview">
        <TabsList className="max-w-full justify-start overflow-x-auto">
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="settings">Settings</TabsTrigger>
          {isAdmin && <TabsTrigger value="actions">Actions</TabsTrigger>}
        </TabsList>
        <TabsContent value="overview" className="mt-2">
          <RecursorOverviewTab rec={rec} live={live.data} />
        </TabsContent>
        <TabsContent value="settings" className="mt-2">
          <RecursorSettingsTab key={rec.desired_config_version} rec={rec} />
        </TabsContent>
        {isAdmin && (
          <TabsContent value="actions" className="mt-2">
            <RecursorActionsTab rec={rec} />
          </TabsContent>
        )}
      </Tabs>
    </>
  )
}
