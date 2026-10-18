import { LuArrowUpFromLine, LuPower, LuRotateCw, LuTrash2 } from 'react-icons/lu'
import type { IconType } from 'react-icons'
import type { ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { useDeleteRecursor, useRecursorCommand } from '@/lib/api/client'
import type { CommandType, Recursor } from '@/lib/api/types'
import { cn } from '@/lib/utils'

import { kindLabel } from './kinds'

function Action({ icon: Icon, title, description, children, danger }: { icon: IconType; title: string; description: string; children: ReactNode; danger?: boolean }) {
  return (
    <Card className={cn('py-4', danger && 'border-destructive/40')}>
      <CardContent className="flex flex-wrap items-center gap-4 px-4">
        <span className={cn('flex size-9 items-center justify-center rounded-md', danger ? 'bg-destructive/10 text-destructive' : 'bg-primary/10 text-primary')}>
          <Icon className="size-4" />
        </span>
        <div className="min-w-0 flex-1">
          <p className="font-medium">{title}</p>
          <p className="text-sm text-muted-foreground">{description}</p>
        </div>
        {children}
      </CardContent>
    </Card>
  )
}

export function RecursorActionsTab({ rec }: { rec: Recursor }) {
  const cmd = useRecursorCommand()
  const del = useDeleteRecursor()
  const nav = useNavigate()
  const label = kindLabel(rec.kind)

  const run = (type: CommandType, what: string) =>
    cmd.mutateAsync({ id: rec.id, type }).then(
      () => toast.success(`${what} queued`, { description: 'The agent runs it on its next heartbeat.' }),
      (e: Error) => toast.error(e.message),
    )

  const remove = () =>
    del.mutateAsync(rec.id).then(
      () => {
        toast.success(`Recursor ${rec.name} deleted`)
        nav('/recursors')
      },
      (e: Error) => toast.error(e.message),
    )

  return (
    <div className="max-w-3xl space-y-3">
      <Action icon={LuRotateCw} title="Reapply configuration" description="Re-renders the resolver config and restarts it, with automatic rollback if it does not come back.">
        <ConfirmDialog title="Reapply configuration?" description={`${label} restarts and its cache is emptied.`} confirmLabel="Reapply" onConfirm={() => run('reapply', 'Reapply')}>
          <Button variant="outline">Reapply</Button>
        </ConfirmDialog>
      </Action>
      <Action icon={LuPower} title={`Restart ${label}`} description="Nodes fail over to their other upstreams for a few seconds; the cache starts empty.">
        <ConfirmDialog title={`Restart ${label}?`} description={`${label} on ${rec.name} stops answering briefly.`} confirmLabel="Restart" onConfirm={() => run('restart_resolver', 'Restart')}>
          <Button variant="outline">Restart</Button>
        </ConfirmDialog>
      </Action>
      <Action
        icon={LuArrowUpFromLine}
        title="Upgrade agent"
        description={rec.agent_outdated ? `Runs ${rec.agent_version}; this panel ships a newer agent.` : `Runs ${rec.agent_version || '—'}, the panel's version.`}
      >
        <ConfirmDialog title="Upgrade the agent?" description="The agent replaces itself and restarts; the resolver keeps answering." confirmLabel="Upgrade" onConfirm={() => run('upgrade_agent', 'Agent upgrade')}>
          <Button variant="outline" disabled={!rec.agent_outdated}>
            Upgrade
          </Button>
        </ConfirmDialog>
      </Action>
      <Action danger icon={LuTrash2} title="Delete recursor" description={`Removes it from the panel and revokes its token. ${label} keeps running with its last config.`}>
        <ConfirmDialog
          destructive
          title={`Delete ${rec.name}?`}
          description="The agent loses access immediately. Profiles that use it as an upstream keep its address until you edit them."
          confirmLabel="Delete recursor"
          onConfirm={remove}
        >
          <Button variant="destructive">Delete</Button>
        </ConfirmDialog>
      </Action>
    </div>
  )
}
