import type { IconType } from 'react-icons'
import {
  LuChartNoAxesColumn,
  LuFileChartColumn,
  LuLayoutDashboard,
  LuPackageCheck,
  LuScrollText,
  LuServer,
  LuSettings,
  LuShieldBan,
  LuSiren,
  LuSlidersHorizontal,
  LuUsers,
} from 'react-icons/lu'
import { Link, Outlet, useLocation, useMatch, useMatches } from 'react-router'

import { Separator } from '@/components/ui/separator'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarRail,
  SidebarTrigger,
  useSidebar,
} from '@/components/ui/sidebar'
import { useOverview } from '@/lib/api/client'
import { fmtQps } from '@/lib/format'
import { cn } from '@/lib/utils'
import { useAuth } from './auth'
import { BrandHead, BrandIcon, Cloud, useBranding } from './branding'
import { ThemeToggle } from './theme-toggle'
import { UserMenu } from './user-menu'

interface NavItem {
  to: string
  label: string
  icon: IconType
}

const groups: { label: string; items: NavItem[]; admin?: boolean }[] = [
  {
    label: 'Monitoring',
    items: [
      { to: '/', label: 'Overview', icon: LuLayoutDashboard },
      { to: '/nodes', label: 'Nodes', icon: LuServer },
      { to: '/analytics', label: 'Analytics', icon: LuChartNoAxesColumn },
      { to: '/offenders', label: 'Offenders', icon: LuSiren },
    ],
  },
  {
    label: 'Configuration',
    items: [
      { to: '/profiles', label: 'Profiles', icon: LuSlidersHorizontal },
      { to: '/blocklist', label: 'Blocklist', icon: LuShieldBan },
      { to: '/upgrades', label: 'Upgrades', icon: LuPackageCheck },
    ],
  },
  { label: 'Compliance', items: [{ to: '/reports', label: 'Reports', icon: LuFileChartColumn }] },
  {
    label: 'Admin',
    admin: true,
    items: [
      { to: '/users', label: 'Users', icon: LuUsers },
      { to: '/audit', label: 'Audit', icon: LuScrollText },
      { to: '/settings', label: 'Settings', icon: LuSettings },
    ],
  },
]

// Active item: a verdigris hairline on the left edge, the row gently lifted, the icon in the accent.
const activeBar =
  "relative transition-colors duration-500 ease-(--ease-lux) [&>svg]:transition-colors [&>svg]:duration-500 data-[active=true]:bg-sidebar-accent data-[active=true]:font-medium data-[active=true]:[&>svg]:text-brand-accent data-[active=true]:before:absolute data-[active=true]:before:inset-y-2 data-[active=true]:before:-left-2 data-[active=true]:before:w-px data-[active=true]:before:bg-linear-to-b data-[active=true]:before:from-transparent data-[active=true]:before:via-brand-accent data-[active=true]:before:to-transparent data-[active=true]:before:content-['']"

function NavEntry({ item }: { item: NavItem }) {
  const { isMobile, setOpenMobile } = useSidebar()
  const active = !!useMatch({ path: item.to, end: item.to === '/' })
  return (
    <SidebarMenuItem>
      <SidebarMenuButton asChild isActive={active} tooltip={item.label} className={activeBar}>
        <Link to={item.to} onClick={() => isMobile && setOpenMobile(false)}>
          <item.icon />
          <span>{item.label}</span>
        </Link>
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
}

function Brand() {
  const { name, assets } = useBranding()
  return (
    <Link to="/" aria-label={`${name} home`} className="flex h-12 items-center gap-2 rounded-md px-1 outline-hidden focus-visible:ring-2 focus-visible:ring-sidebar-ring">
      <BrandIcon className={assets.navbar_dark ? 'hidden group-data-[collapsible=icon]:block' : ''} />
      {assets.navbar_dark ? (
        <img src={assets.navbar_dark} alt="" className="h-11 w-auto max-w-full object-contain object-left group-data-[collapsible=icon]:hidden" />
      ) : (
        <span className="flex min-w-0 flex-col leading-none group-data-[collapsible=icon]:hidden">
          <span className="truncate font-display text-[1.4rem]">{name}</span>
          <span className="mt-1 text-[9.5px] tracking-[0.28em] text-brand-accent/80 uppercase">Fleet control</span>
        </span>
      )}
    </Link>
  )
}

/** x/y nodes online; amber when any node is degraded, red when any is offline. */
function FleetPill() {
  const { data } = useOverview()
  if (!data) return null
  const n = data.nodes
  const tone = n.offline
    ? 'border-red-300/30 bg-red-400/10 text-red-100 [--dot:var(--color-red-300)]'
    : n.degraded
      ? 'border-amber-200/30 bg-amber-300/10 text-amber-50 [--dot:var(--color-amber-200)]'
      : 'border-brand-accent/25 bg-brand-accent/5 text-brand-ink-foreground [--dot:var(--brand-accent)]'
  return (
    <Link
      to="/nodes"
      className={cn(
        'flex items-center gap-1 rounded-full border px-2.5 py-1 text-xs font-medium whitespace-nowrap outline-hidden hover:brightness-125 focus-visible:ring-2 focus-visible:ring-brand-accent',
        tone,
      )}
    >
      <span aria-hidden className="mr-0.5 size-1.5 rounded-full bg-(--dot)" />
      <span className="tabular">
        {n.online ?? 0}/{data.nodes_total}
      </span>
      <span className="sr-only sm:not-sr-only">nodes online</span>
    </Link>
  )
}

/** Sidebar footer: fleet availability as a serif figure over a hairline gauge. */
function FleetSummary() {
  const { data } = useOverview()
  if (!data) return null
  const online = data.nodes.online ?? 0
  const pct = data.nodes_total ? (online / data.nodes_total) * 100 : 0
  return (
    <Link
      to="/nodes"
      className="block space-y-2.5 rounded-lg border border-sidebar-border bg-sidebar/85 p-3 backdrop-blur-sm outline-hidden transition-colors duration-500 hover:border-brand-accent/35 focus-visible:ring-2 focus-visible:ring-sidebar-ring"
    >
      <span className="flex items-baseline justify-between">
        <span className="text-[9.5px] tracking-[0.28em] text-brand-ink-muted uppercase">Fleet</span>
        <span className="text-[11px] text-brand-ink-muted tabular">{fmtQps(data.qps)}</span>
      </span>
      <span className="block font-display text-3xl leading-none tabular">
        {online}
        <span className="text-lg text-brand-ink-muted"> / {data.nodes_total} online</span>
      </span>
      <span className="block h-px bg-white/10">
        <span className="block h-px bg-linear-to-r from-brand-accent/40 to-brand-accent transition-[width] duration-1000 ease-(--ease-lux)" style={{ width: `${pct}%` }} />
      </span>
    </Link>
  )
}

/** Page title comes from the matched route's `handle.title` (see routes.tsx). */
function useTitle() {
  const m = useMatches()
  for (let i = m.length - 1; i >= 0; i--) {
    const t = (m[i].handle as { title?: string } | undefined)?.title
    if (t) return t
  }
  return ''
}

// Controls sitting on the navy top bar.
const onNavy = 'text-brand-ink-foreground hover:bg-white/10 hover:text-white dark:hover:bg-white/10 focus-visible:ring-brand-accent/60'

export function AppLayout() {
  const { isAdmin } = useAuth()
  const title = useTitle()
  const { pathname } = useLocation()
  const group = groups.find((g) => g.items.some((it) => (it.to === '/' ? pathname === '/' : pathname.startsWith(it.to))))?.label
  return (
    <SidebarProvider>
      <BrandHead title={title} />
      <Sidebar collapsible="icon">
        <SidebarHeader className="border-b border-sidebar-border">
          <Brand />
        </SidebarHeader>
        <SidebarContent className="relative z-10">
          {groups
            .filter((g) => isAdmin || !g.admin)
            .map((g) => (
              <SidebarGroup key={g.label}>
                <SidebarGroupLabel className="text-[9.5px] tracking-[0.28em] text-brand-ink-muted/80 uppercase">{g.label}</SidebarGroupLabel>
                <SidebarGroupContent>
                  <SidebarMenu>
                    {g.items.map((it) => (
                      <NavEntry key={it.to} item={it} />
                    ))}
                  </SidebarMenu>
                </SidebarGroupContent>
              </SidebarGroup>
            ))}
        </SidebarContent>
        <SidebarFooter className="relative z-10 group-data-[collapsible=icon]:hidden">
          <FleetSummary />
        </SidebarFooter>
        <div aria-hidden className="pointer-events-none absolute inset-x-0 top-0 h-56 bg-[radial-gradient(ellipse_at_10%_0%,oklch(0.82_0.065_178/0.09),transparent_70%)]" />
        <Cloud className="bottom-0 left-0 w-full opacity-15 group-data-[collapsible=icon]:hidden" />
        <SidebarRail />
      </Sidebar>
      <SidebarInset className="min-w-0">
        <header className="sticky top-0 z-20 flex h-12 shrink-0 items-center gap-2 overflow-hidden bg-brand-ink/95 px-3 text-brand-ink-foreground backdrop-blur">
          <span aria-hidden className="absolute inset-x-0 bottom-0 h-px bg-linear-to-r from-transparent via-brand-accent/35 to-transparent" />
          <Cloud className="top-1/2 right-0 w-64 -translate-y-1/2 opacity-15" />
          <SidebarTrigger className={cn('-ml-1', onNavy)} />
          <Separator orientation="vertical" className="mr-1 bg-white/20 data-[orientation=vertical]:h-4" />
          <h2 className="flex min-w-0 items-baseline gap-2 truncate text-sm font-medium">
            {group && <span className="hidden text-[10px] tracking-[0.24em] text-brand-ink-muted uppercase sm:inline">{group}</span>}
            {group && <span aria-hidden className="hidden text-brand-accent/60 sm:inline">·</span>}
            <span key={title} className="animate-in font-display text-xl duration-700 ease-(--ease-lux) fade-in">
              {title}
            </span>
          </h2>
          <div className="relative ml-auto flex items-center gap-1">
            <FleetPill />
            <ThemeToggle className={onNavy} />
            <UserMenu className={onNavy} />
          </div>
        </header>
        {/* SidebarInset is already the <main> landmark */}
        <div key={pathname} className="stagger mx-auto w-full max-w-[1600px] flex-1 space-y-6 p-4 md:p-6">
          <Outlet />
        </div>
      </SidebarInset>
    </SidebarProvider>
  )
}
