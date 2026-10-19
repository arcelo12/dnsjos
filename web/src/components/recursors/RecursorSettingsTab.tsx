import { useState } from 'react'
import { toast } from 'sonner'

import { useAuth } from '@/app/auth'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { useUpdateRecursor } from '@/lib/api/client'
import type { Recursor, RecursorSettings } from '@/lib/api/types'

const lines = (s: string) =>
  s
    .split(/[\n,]/)
    .map((l) => l.trim())
    .filter(Boolean)

/** Panel-managed resolver settings; the agent applies them within seconds of saving. */
export function RecursorSettingsTab({ rec }: { rec: Recursor }) {
  const { isAdmin } = useAuth()
  const update = useUpdateRecursor()
  const [allow, setAllow] = useState(rec.settings.allow_extra.join('\n'))
  const [ecs, setEcs] = useState(rec.settings.ecs)
  const [zones, setZones] = useState(rec.settings.ecs_zones.join('\n'))
  const [cache, setCache] = useState(String(rec.settings.cache_mb))
  const [publicIP, setPublicIP] = useState(rec.public_ip)
  const ro = !isAdmin

  const save = (e: React.FormEvent) => {
    e.preventDefault()
    const settings: RecursorSettings = { allow_extra: lines(allow), ecs, ecs_zones: ecs ? lines(zones) : [], cache_mb: Math.trunc(Number(cache)) || 0 }
    update.mutate(
      { id: rec.id, patch: { settings, public_ip: publicIP.trim() } },
      {
        onSuccess: () => toast.success('Settings saved', { description: 'The agent applies them with its next config poll.' }),
        onError: (err) => toast.error(err.message),
      },
    )
  }

  const automatic = rec.allow.filter((a) => !rec.settings.allow_extra.some((x) => a === x || a === `${x}/32` || a === `${x}/128`))

  return (
    <form onSubmit={save} className="grid max-w-3xl gap-4">
      <Card className="gap-3 py-4">
        <CardHeader className="px-4">
          <CardTitle className="text-sm">Who may query it</CardTitle>
          <CardDescription>
            Every DnsJos node is allowed automatically (its public IPv4 and IPv6); everyone else is refused. Add other
            addresses or networks below, one per line.
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-3 px-4">
          <div className="grid gap-1.5">
            <Label htmlFor="rec-allow">Extra addresses / CIDRs</Label>
            <Textarea id="rec-allow" rows={3} readOnly={ro} value={allow} onChange={(e) => setAllow(e.target.value)} placeholder={'203.0.113.10\n10.20.0.0/16'} className="font-mono text-xs" />
            <p className="text-xs text-muted-foreground">Public networks need at least /16 (IPv4) or /32 (IPv6); private ranges (10/8, 172.16/12, 192.168/16, 100.64/10, fc00::/7) may be added whole.</p>
          </div>
          <details className="text-xs">
            <summary className="cursor-pointer text-muted-foreground">Automatic entries ({automatic.length})</summary>
            <ul className="mt-2 grid grid-cols-2 gap-1 font-mono sm:grid-cols-3">
              {automatic.map((a) => (
                <li key={a}>{a}</li>
              ))}
            </ul>
          </details>
        </CardContent>
      </Card>

      <Card className="gap-3 py-4">
        <CardHeader className="px-4">
          <CardTitle className="text-sm">Client subnet (ECS)</CardTitle>
          <CardDescription>
            Pass the client subnet dnsdist adds on to authoritative servers, so CDNs answer for the customer's location. Also
            turn ECS on for this upstream in the profile.
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-3 px-4">
          <div className="flex items-center gap-2">
            <Switch id="rec-ecs" checked={ecs} disabled={ro} onCheckedChange={setEcs} />
            <Label htmlFor="rec-ecs">ECS on</Label>
          </div>
          {ecs && (
            <div className="grid gap-1.5">
              <Label htmlFor="rec-zones">Only for these zones</Label>
              <Textarea id="rec-zones" rows={3} readOnly={ro} value={zones} onChange={(e) => setZones(e.target.value)} placeholder={'googlevideo.com\nakamaiedge.net'} className="font-mono text-xs" />
              <p className="text-xs text-muted-foreground">Empty: every authoritative server gets it, as public resolvers do.</p>
            </div>
          )}
        </CardContent>
      </Card>

      <Card className="gap-3 py-4">
        <CardHeader className="px-4">
          <CardTitle className="text-sm">Resolver</CardTitle>
          <CardDescription>DNSSEC validation, prefetching and serve-stale are always on.</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 px-4 sm:grid-cols-2">
          <div className="grid gap-1.5">
            <Label htmlFor="rec-cache">Cache size (MB)</Label>
            <Input id="rec-cache" type="number" min={0} max={65536} readOnly={ro} value={cache} onChange={(e) => setCache(e.target.value)} />
            <p className="text-xs text-muted-foreground">0 = sized from the host's RAM.</p>
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="rec-ip">Public IP</Label>
            <Input id="rec-ip" readOnly={ro} value={publicIP} onChange={(e) => setPublicIP(e.target.value)} className="font-mono" />
            <p className="text-xs text-muted-foreground">The address nodes query. An IPv4 one is also sent as the location of queries without a client subnet.</p>
          </div>
        </CardContent>
      </Card>

      {!ro && (
        <Button type="submit" className="w-fit" disabled={update.isPending}>
          Save settings
        </Button>
      )}
    </form>
  )
}
