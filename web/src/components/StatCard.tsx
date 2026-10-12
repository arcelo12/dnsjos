import type { ReactNode } from 'react'
import type { IconType } from 'react-icons'
import { LuTrendingDown, LuTrendingUp } from 'react-icons/lu'

import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

export interface StatCardProps {
  title: string
  value: ReactNode
  icon?: IconType
  hint?: ReactNode
  /** Relative change, e.g. 0.12 = +12%. Colour follows `trendGood`. */
  trend?: number
  trendGood?: 'up' | 'down'
  loading?: boolean
  className?: string
}

export function StatCard({ title, value, icon: Icon, hint, trend, trendGood = 'up', loading, className }: StatCardProps) {
  const up = (trend ?? 0) >= 0
  const good = up === (trendGood === 'up')
  return (
    <Card className={cn('group/stat relative gap-0 overflow-hidden py-5 hover:border-primary/35 hover:shadow-[0_18px_40px_-24px_color-mix(in_oklch,var(--primary)_60%,transparent)]', className)}>
      {/* A hairline draws across the top edge on hover. */}
      <span aria-hidden className="absolute inset-x-0 top-0 h-px origin-left scale-x-0 bg-linear-to-r from-transparent via-primary/70 to-transparent transition-transform duration-700 ease-(--ease-lux) group-hover/stat:scale-x-100" />
      <CardContent className="@container px-4">
        <div className="flex items-center justify-between gap-2">
          <span className="text-[10px] font-medium tracking-[0.2em] text-muted-foreground uppercase">{title}</span>
          {Icon && (
            <span className="flex size-7 items-center justify-center rounded-full border border-primary/20 text-primary/80 transition-colors duration-500 group-hover/stat:border-primary/50 group-hover/stat:text-primary">
              <Icon className="size-3.5" />
            </span>
          )}
        </div>
        {loading ? (
          <Skeleton className="mt-3 h-8 w-24" />
        ) : (
          <div className="tabular mt-3 font-display text-[clamp(1.4rem,18cqi,2.1rem)] leading-none whitespace-nowrap">{value}</div>
        )}
        {(hint || trend !== undefined) && (
          <div className="mt-2 flex items-center gap-2 text-xs text-muted-foreground">
            {trend !== undefined && Number.isFinite(trend) && (
              <span className={cn('inline-flex items-center gap-0.5 font-medium', good ? 'text-success' : 'text-destructive')}>
                {up ? <LuTrendingUp className="size-3.5" /> : <LuTrendingDown className="size-3.5" />}
                {Math.abs(trend * 100).toFixed(1)}%
              </span>
            )}
            {hint && <span className="truncate">{hint}</span>}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
