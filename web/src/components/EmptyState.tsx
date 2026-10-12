import type { ReactNode } from 'react'
import type { IconType } from 'react-icons'
import { LuInbox } from 'react-icons/lu'

import { cn } from '@/lib/utils'

export function EmptyState({
  icon: Icon = LuInbox,
  title,
  description,
  action,
  className,
}: {
  icon?: IconType
  title: string
  description?: ReactNode
  action?: ReactNode
  className?: string
}) {
  return (
    <div className={cn('flex flex-col items-center justify-center gap-2 px-4 py-10 text-center', className)}>
      <span className="flex size-12 items-center justify-center rounded-full border border-primary/25 text-primary/80 ring-8 ring-primary/[0.04]">
        <Icon className="size-5" />
      </span>
      <p className="font-display text-2xl">{title}</p>
      {description && <p className="max-w-sm text-sm text-muted-foreground">{description}</p>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  )
}
