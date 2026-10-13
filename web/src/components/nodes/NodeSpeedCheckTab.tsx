import { LuGauge } from 'react-icons/lu'

import { EmptyState } from '@/components/EmptyState'
import { TimeAgo } from '@/components/TimeAgo'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { useNodeSpeedCheck } from '@/lib/api/client'

const speed = (v: number) => (v < 0 ? 'no answer' : `${v.toFixed(1)} ms`)

/** SPEC §6.9: the qnames this node speed-checks for fastest-IP answers, with their fastest address. */
export function NodeSpeedCheckTab({ id }: { id: string }) {
  const q = useNodeSpeedCheck(id)
  const d = q.data
  const items = d?.names ?? []
  const reported = d && !d.at.startsWith('0001-')
  return (
    <Card className="gap-3 py-4">
      <CardHeader className="px-4">
        <CardTitle className="flex items-center gap-2 text-sm">Speed-checked names ({items.length})</CardTitle>
        <CardDescription className="text-xs">
          The busiest names with multi-address answers, measured every 10 minutes with the profile's speed-check mode
          (fastest-IP answers). &ldquo;Fastest&rdquo; is the address that answered first from this node; answers are rebuilt so it
          comes first.
          {reported && (
            <>
              {' '}
              {d.checked} names measured in total, last report <TimeAgo date={d.at} />.
            </>
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="px-4">
        {q.isPending ? (
          <Skeleton className="h-24" />
        ) : !items.length ? (
          <EmptyState
            icon={LuGauge}
            title={reported ? 'Nothing measured yet' : 'No report yet'}
            description={
              !reported
                ? 'Enable fastest-IP answers in the profile (or a node override); the agent reports within 10 minutes.'
                : 'No measured names with answering addresses yet; the busiest names appear as clients ask for them.'
            }
          />
        ) : (
          <div className="overflow-x-auto rounded-md border">
            <table className="w-full text-sm">
              <thead className="bg-muted/50 text-xs text-muted-foreground">
                <tr>
                  <th className="px-3 py-2 text-left font-medium">Qname</th>
                  <th className="px-3 py-2 text-left font-medium">Fastest address</th>
                  <th className="px-3 py-2 text-right font-medium">Time</th>
                  <th className="px-3 py-2 text-right font-medium">Addresses</th>
                  <th className="px-3 py-2 text-right font-medium">Hits</th>
                  <th className="px-3 py-2 text-right font-medium">Checked</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {items.map((e) => (
                  <tr key={e.name}>
                    <td className="px-3 py-2 font-mono text-xs">{e.name}</td>
                    <td className="px-3 py-2 font-mono text-xs">{e.top_ip || <span className="text-muted-foreground">&mdash;</span>}</td>
                    <td className="px-3 py-2 text-right text-xs tabular-nums">{speed(e.top_ms)}</td>
                    <td className="px-3 py-2 text-right text-xs tabular-nums">{e.addresses}</td>
                    <td className="px-3 py-2 text-right tabular-nums">{e.hits.toLocaleString()}</td>
                    <td className="px-3 py-2 text-right text-xs text-muted-foreground">
                      <TimeAgo date={e.checked_at} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
