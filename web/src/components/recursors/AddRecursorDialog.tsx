import { useState } from 'react'
import { LuCircleCheck, LuPlus } from 'react-icons/lu'
import { toast } from 'sonner'

import { CopyButton } from '@/components/CopyButton'
import { TimeAgo } from '@/components/TimeAgo'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useCreateRecursorToken } from '@/lib/api/client'
import { RECURSOR_KINDS, type RecursorKind, type RecursorTokenCreated } from '@/lib/api/types'
import { fmtDateTime } from '@/lib/format'

import { KIND_INFO } from './kinds'

export function AddRecursorDialog() {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [kind, setKind] = useState<RecursorKind>('pdns')
  const [ttl, setTtl] = useState('24')
  const [created, setCreated] = useState<RecursorTokenCreated>()
  const create = useCreateRecursorToken()

  const reset = (o: boolean) => {
    setOpen(o)
    if (!o) {
      setName('')
      setKind('pdns')
      setTtl('24')
      setCreated(undefined)
      create.reset()
    }
  }

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    create.mutate(
      { name: name.trim(), kind, ttl_hours: Number(ttl) || 24 },
      {
        onSuccess: (t) => {
          setCreated(t)
          toast.success('Recursor token created')
        },
        onError: (err) => toast.error(err.message),
      },
    )
  }

  return (
    <Dialog open={open} onOpenChange={reset}>
      <DialogTrigger asChild>
        <Button>
          <LuPlus /> Add recursor
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-xl">
        {created ? (
          <>
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2">
                <LuCircleCheck className="text-success" /> Run this on the new recursor
              </DialogTitle>
              <DialogDescription>
                As root on a fresh Debian 12/13 or Ubuntu 22.04/24.04 host (not a node). The token is shown only once and expires{' '}
                <TimeAgo date={created.expires_at} /> ({fmtDateTime(created.expires_at)}).
              </DialogDescription>
            </DialogHeader>
            <div className="flex items-start gap-2 rounded-md border bg-muted/50 p-3">
              <code className="min-w-0 flex-1 font-mono text-xs break-all">{created.install_command}</code>
              <CopyButton value={created.install_command} />
            </div>
            <div className="space-y-1 text-sm text-muted-foreground">
              <p className="font-medium text-foreground">What happens next</p>
              <ol className="list-decimal space-y-0.5 pl-5">
                <li>The installer sets up {KIND_INFO[kind].label} and the DnsJos agent in recursor mode.</li>
                <li>The agent writes the resolver config: DNSSEC on, ECS on, only your nodes allowed to query it.</li>
                <li>It shows up here as online; then pick it as an upstream in a profile.</li>
              </ol>
            </div>
            <DialogFooter>
              <Button onClick={() => reset(false)}>Done</Button>
            </DialogFooter>
          </>
        ) : (
          <form onSubmit={submit} className="grid gap-4">
            <DialogHeader>
              <DialogTitle>Add recursor</DialogTitle>
              <DialogDescription>A recursive resolver on its own host that your nodes use as an upstream.</DialogDescription>
            </DialogHeader>
            <div className="grid gap-2">
              <Label htmlFor="rec-name">Name</Label>
              <Input id="rec-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="rec-jkt-1" required autoFocus />
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="grid gap-2">
                <Label htmlFor="rec-kind">Resolver</Label>
                <Select value={kind} onValueChange={(v) => setKind(v as RecursorKind)}>
                  <SelectTrigger id="rec-kind" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {RECURSOR_KINDS.map((k) => (
                      <SelectItem key={k} value={k}>
                        {KIND_INFO[k].label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="grid gap-2">
                <Label htmlFor="rec-ttl">Token valid for (hours)</Label>
                <Input id="rec-ttl" type="number" min={1} max={720} value={ttl} onChange={(e) => setTtl(e.target.value)} />
              </div>
            </div>
            <p className="text-xs text-muted-foreground">{KIND_INFO[kind].help} The resolver cannot be switched later.</p>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => reset(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={create.isPending || !name.trim()}>
                Create token
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
