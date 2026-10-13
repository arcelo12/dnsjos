import { LuPlus, LuTrash2 } from 'react-icons/lu'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import type { IPAlias, IPAliasRule } from '@/lib/api/types'
import { useFieldErrors, useListErrors, useReadOnly } from './context'
import { ListEditor, Section } from './fields'
import { isAliasPool, isAliasTarget, isHostname, isIPv4 } from './validate'

const blank = (): IPAliasRule => ({ name: '', match_pools: [], targets: [], exclude: [] })

/** Address family of one target ('A'/'AAAA'), or null when it is not a usable plain address. */
const famOf = (t: string): 'A' | 'AAAA' | null => (!isAliasTarget(t) ? null : isIPv4(t) ? 'A' : 'AAAA')

/** Address family of a rule, from targets[0] (what the Go validation and Lua generator use). */
const family = (r: IPAliasRule): 'A' | 'AAAA' | null => (r.targets[0] ? famOf(r.targets[0]) : null)

function Errors({ path }: { path: string }) {
  const errs = useFieldErrors(path)
  return errs.length ? <p className="text-xs text-destructive">{errs.join(' · ')}</p> : null
}

/**
 * One IPAliasRule card. Nested lists (pools/targets/exclude) reuse ListEditor, whose
 * error keys (`ip_alias.rules[i].targets[2]`) are exactly what useListErrors parses.
 */
function RuleCard({ rule, index, onPatch, onRemove }: {
  rule: IPAliasRule
  index: number
  onPatch: (p: Partial<IPAliasRule>) => void
  onRemove: () => void
}) {
  const ro = useReadOnly()
  const p = `ip_alias.rules[${index}]`
  const fam = family(rule)
  const nameErrs = useFieldErrors(`${p}.name`)

  const mixedFamily = rule.targets.length > 1 && fam !== null && rule.targets.some((t) => famOf(t) !== fam)

  return (
    <div className="grid gap-3 rounded-md border p-3">
      <div className="flex items-start justify-between gap-3">
        <div className="grid content-start gap-1.5 sm:max-w-xs sm:flex-1">
          <Label htmlFor={`${p}.name`}>Rule name</Label>
          <Input
            id={`${p}.name`}
            value={rule.name}
            placeholder="cf"
            readOnly={ro}
            aria-invalid={nameErrs.length > 0 || undefined}
            onChange={(e) => onPatch({ name: e.target.value.trim() })}
            className="font-mono text-sm"
          />
          <p className="text-xs text-muted-foreground">
            Labels the rule in metrics and logs; not a DNS name.{' '}
            {fam && (
              <>
                Rewrites <span className="font-mono">{fam}</span> answers.
              </>
            )}
          </p>
          <Errors path={`${p}.name`} />
        </div>
        {!ro && (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-8 text-muted-foreground hover:text-destructive"
            aria-label={`Remove rule ${rule.name || `#${index + 1}`}`}
            onClick={onRemove}
          >
            <LuTrash2 />
          </Button>
        )}
      </div>

      <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
        <ListEditor
          label="Match pools"
          path={`${p}.match_pools`}
          value={rule.match_pools}
          onChange={(match_pools) => onPatch({ match_pools })}
          validate={isAliasPool}
          what="CIDR"
          help="CDN ranges (the ones from the CGK tab work here too). An answer holding an address in one of them is rewritten. IPv4-mapped forms match nothing and are rejected."
        />
        <ListEditor
          label="Targets"
          path={`${p}.targets`}
          value={rule.targets}
          onChange={(targets) => onPatch({ targets })}
          validate={isAliasTarget}
          what="IP address"
          help="Preferred edge addresses, used round-robin. All must be one family; targets[0] picks A (IPv4) or AAAA (IPv6). Plain addresses only — no zones, IPv4-mapped or dotted-tail forms."
        />
        <ListEditor
          label="Exclude"
          path={`${p}.exclude`}
          value={rule.exclude}
          onChange={(exclude) => onPatch({ exclude })}
          validate={isHostname}
          what="domain"
          help="Names (and their subdomains) this rule never rewrites — tunnels, ACME, mail."
        />
      </div>
      {mixedFamily && <p className="text-xs text-destructive">All targets must be the same address family as targets[0].</p>}
    </div>
  )
}

/** Nested editor for ip_alias (SPEC §6.12): rules that pin CDN answers to chosen edge IPs. */
export function IPAliasEditor({ value, onChange }: { value: IPAlias; onChange: (v: IPAlias) => void }) {
  const ro = useReadOnly()
  const rules = value.rules
  const setRules = (r: IPAliasRule[]) => onChange({ ...value, rules: r })
  const patch = (i: number, p: Partial<IPAliasRule>) => setRules(rules.map((r, j) => (j === i ? { ...r, ...p } : r)))
  const listErrs = useListErrors('ip_alias.rules').get(-1) ?? []

  return (
    <Section
      title="IP-alias"
      description="smartdns ip-rules ip-set aliasing: an A/AAAA answer whose address is inside a rule's match pools is rewritten to the rule's targets, so clients always reach the chosen edge (a PoP that peers better, a scrubbing centre) no matter which address the upstream returned. The mirror image of CGK: CGK measures to pick an edge, IP-alias forces one."
      enabled={value.enabled}
      onEnabledChange={(enabled) => onChange({ ...value, enabled })}
    >
      {rules.map((r, i) => (
        <RuleCard key={i} rule={r} index={i} onPatch={(p) => patch(i, p)} onRemove={() => setRules(rules.filter((_, j) => j !== i))} />
      ))}
      {rules.length === 0 && (
        <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
          No rules — nothing is rewritten.
        </p>
      )}
      {listErrs.length > 0 && <p className="text-xs text-destructive">{listErrs.join(' · ')}</p>}
      {!ro && (
        <Button type="button" variant="outline" className="w-fit" onClick={() => setRules([...rules, blank()])}>
          <LuPlus /> Add rule
        </Button>
      )}
    </Section>
  )
}
