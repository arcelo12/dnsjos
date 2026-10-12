import type { ReactNode } from 'react'

/** `identifier`: the title is a name like dns1, so it stays in the sans (the serif's 1 reads as l). */
export function PageHeader({ title, description, actions, identifier }: { title: string; description?: ReactNode; actions?: ReactNode; identifier?: boolean }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-3 border-b border-border/70 pb-5">
      <div className="min-w-0 space-y-2">
        <h1 className={identifier ? 'font-sans text-3xl leading-none font-semibold tracking-tight' : 'text-4xl leading-none'}>{title}</h1>
        {description && <p className="text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}
