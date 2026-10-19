import { LuDatabase } from 'react-icons/lu'

import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { StatusBadge } from '@/components/StatusBadge'
import { useRecursors } from '@/lib/api/client'
import type { Recursor, Upstream } from '@/lib/api/types'

import { kindLabel } from './kinds'

/** The upstream a recursor stands for: its public address on :53, ECS as the recursor has it. */
const recursorUpstream = (r: Recursor): Upstream => ({
  name: r.name,
  address: r.public_ip.includes(':') ? `[${r.public_ip}]:53` : `${r.public_ip}:53`,
  weight: 10,
  order: 1,
  sockets: 4,
  ecs: r.settings.ecs,
})

/** "Add recursor" next to "Add upstream": picks one of the panel's recursors. */
export function RecursorPicker({ servers, onAdd }: { servers: Upstream[]; onAdd: (u: Upstream) => void }) {
  const recursors = useRecursors()
  const items = (recursors.data?.items ?? []).filter((r) => r.public_ip)
  const used = new Set(servers.map((s) => s.address))
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button type="button" variant="outline" className="w-fit">
          <LuDatabase /> Add recursor
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="min-w-72">
        <DropdownMenuLabel className="text-xs text-muted-foreground">Recursors managed by this panel</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {items.length === 0 && (
          <DropdownMenuItem disabled className="text-xs">
            {recursors.isPending ? 'Loading…' : 'None yet: add one under Recursors.'}
          </DropdownMenuItem>
        )}
        {items.map((r) => {
          const u = recursorUpstream(r)
          return (
            <DropdownMenuItem key={r.id} disabled={used.has(u.address)} onSelect={() => onAdd(u)} className="flex items-center gap-2">
              <span className="min-w-0 flex-1">
                <span className="block font-medium">{r.name}</span>
                <span className="block font-mono text-xs text-muted-foreground">
                  {u.address} · {kindLabel(r.kind)} · ECS {r.settings.ecs ? 'on' : 'off'}
                  {used.has(u.address) && ' · already added'}
                </span>
              </span>
              <StatusBadge status={r.status} />
            </DropdownMenuItem>
          )
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
