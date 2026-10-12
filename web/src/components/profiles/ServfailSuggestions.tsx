import { LuExternalLink, LuPlus } from 'react-icons/lu'

import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { useAnalytics } from '@/lib/api/client'

/** True when name equals or is a subdomain of a listed suffix. */
const covered = (name: string, suffixes: string[]) =>
  suffixes.some((s) => {
    s = s.toLowerCase().replace(/\.$/, '')
    return name === s || name.endsWith('.' + s)
  })

/**
 * Top SERVFAIL names of the last 7 days (all nodes) with one-click "add to exceptions". Not every
 * SERVFAIL is DNSSEC (dead or lame name servers fail too), so each name links to DNSViz to check.
 */
export function ServfailSuggestions({ exclude, onAdd }: { exclude: string[]; onAdd: (name: string) => void }) {
  const { data } = useAnalytics({ kind: 'servfail', limit: 50 })
  const names = (data?.top ?? [])
    .map((t) => ({ ...t, name: t.name.toLowerCase().replace(/\.$/, '') }))
    .filter((t) => t.name && !covered(t.name, exclude))
    .slice(0, 15)
  if (!names.length) return null

  return (
    <div className="grid gap-2">
      <Label>Failing with SERVFAIL (last 7 days)</Label>
      <p className="text-xs text-muted-foreground">
        Check a name on DNSViz first: only add it when its DNSSEC is broken, a dead name server fails the same way without it.
      </p>
      <ul className="divide-y rounded-md border text-sm">
        {names.map((t) => (
          <li key={t.name} className="flex items-center gap-2 px-3 py-1.5">
            <span className="min-w-0 flex-1 truncate font-mono text-xs">{t.name}</span>
            <span className="text-xs text-muted-foreground tabular-nums">{t.count.toLocaleString()}</span>
            <Button variant="ghost" size="icon" className="size-7" asChild>
              <a href={`https://dnsviz.net/d/${encodeURIComponent(t.name)}/dnssec/`} target="_blank" rel="noreferrer" aria-label={`Check ${t.name} on DNSViz`}>
                <LuExternalLink />
              </a>
            </Button>
            <Button type="button" variant="outline" size="sm" className="h-7" onClick={() => onAdd(t.name)}>
              <LuPlus /> Add
            </Button>
          </li>
        ))}
      </ul>
    </div>
  )
}
