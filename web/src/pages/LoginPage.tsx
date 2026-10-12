import { useState, type FormEvent } from 'react'
import { LuEye, LuEyeOff, LuLoaderCircle, LuLock, LuMail } from 'react-icons/lu'
import { Navigate, useNavigate, useSearchParams } from 'react-router'

import { useAuth } from '@/app/auth'
import { BrandHead, BrandIcon, Cloud, Orbits, useBranding } from '@/app/branding'
import { ThemeToggle } from '@/app/theme-toggle'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ApiError, useLogin } from '@/lib/api/client'
import { cn } from '@/lib/utils'

/** Only same-origin paths, so ?next= cannot be used as an open redirect. */
function safeNext(next: string | null) {
  return next && next.startsWith('/') && !next.startsWith('//') ? next : '/'
}

export default function LoginPage() {
  const { user } = useAuth()
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const login = useLogin()
  const [error, setError] = useState('')
  const [showPw, setShowPw] = useState(false)
  const { name, tagline, assets } = useBranding()
  const next = safeNext(params.get('next'))

  if (user) return <Navigate to={next} replace />

  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault()
    const f = new FormData(e.currentTarget)
    setError('')
    login.mutate(
      { email: String(f.get('email')).trim(), password: String(f.get('password')) },
      {
        onSuccess: () => navigate(next, { replace: true }),
        onError: (err) =>
          setError(
            err instanceof ApiError && err.status === 401
              ? 'Invalid email or password.'
              : err instanceof ApiError && err.status === 429
                ? 'Too many attempts. Wait a minute and try again.'
                : err.message || 'Sign-in failed.',
          ),
      },
    )
  }

  const invalid = error !== '' || undefined
  // With brand artwork the art is the hero: no headline, the card sits in the art's open space.
  const branded = !!assets.login_bg
  return (
    <main className="grain relative isolate flex min-h-svh overflow-hidden bg-brand-ink text-brand-ink-foreground">
      <BrandHead />
      {assets.login_bg ? (
        <picture className="absolute inset-0 -z-10">
          {assets.login_bg_mobile && <Sources url={assets.login_bg_mobile} media="(max-width: 767px)" />}
          <Sources url={assets.login_bg} />
          {/* A slow settle (30 s, once) makes the artwork feel alive without ever looping. */}
          <img src={jpg(assets.login_bg)} alt="" className="size-full animate-settle object-cover object-center md:object-[72%_50%]" />
        </picture>
      ) : (
        <>
          {/* Two slow-drifting pools of light over midnight: depth without decoration. */}
          <div aria-hidden className="absolute -inset-[20%] -z-10 animate-drift bg-[radial-gradient(ellipse_40%_35%_at_72%_40%,color-mix(in_oklch,var(--brand-accent)_16%,transparent),transparent),radial-gradient(ellipse_35%_30%_at_60%_75%,color-mix(in_oklch,var(--brand-accent)_10%,transparent),transparent)]" />
          <Orbits className="absolute top-1/2 left-1/2 -z-10 w-[140vmin] -translate-1/2 opacity-60 md:left-[70%] md:w-[min(112vmin,1040px)] md:opacity-100" />
        </>
      )}
      <div className="absolute top-4 right-4 z-10">
        <ThemeToggle className="text-brand-ink-foreground hover:bg-white/10 hover:text-white dark:hover:bg-white/10" />
      </div>

      <div
        className={cn(
          'flex w-full flex-col justify-center gap-10 px-5 py-10 md:flex-row md:items-center md:px-10',
          branded ? 'md:justify-start md:pl-[7vw]' : 'mx-auto max-w-6xl md:justify-between',
        )}
      >
        <section className={cn('hidden max-w-xl space-y-8', !branded && 'md:block')}>
          <p className="flex animate-rise items-center gap-3 text-[11px] font-medium tracking-[0.32em] text-brand-accent uppercase">
            <span className="h-px w-10 bg-linear-to-r from-transparent to-brand-accent" />
            {name}
          </p>
          <h1 className="animate-rise font-display text-7xl leading-[0.92] text-balance [animation-delay:90ms] lg:text-[5.5rem]">
            Every query,
            <br />
            <em className="bg-linear-to-r from-brand-accent via-brand-ink-foreground to-brand-accent bg-clip-text pr-2 text-transparent">accounted for.</em>
          </h1>
          <p className="max-w-sm animate-rise text-[15px] leading-relaxed text-brand-ink-muted [animation-delay:180ms]">{tagline}</p>
          <dl className="grid max-w-md animate-rise grid-cols-3 border-t border-brand-accent/15 pt-5 [animation-delay:270ms]">
            {[
              ['i', 'Monitor the fleet'],
              ['ii', 'Ship config safely'],
              ['iii', 'Block once, everywhere'],
            ].map(([n, t]) => (
              <div key={n} className="space-y-1 pr-4">
                <dt className="font-display text-xl text-brand-accent italic">{n}.</dt>
                <dd className="text-xs leading-snug text-brand-ink-foreground/75">{t}</dd>
              </div>
            ))}
          </dl>
        </section>

        <div className="relative mx-auto w-full max-w-sm animate-rise [animation-delay:120ms] md:mx-0">
          <Cloud className="-right-24 -bottom-14 w-80 animate-float opacity-70" />
          <div aria-hidden className="absolute -inset-px rounded-[1.1rem] bg-linear-to-b from-brand-accent/45 via-brand-accent/5 to-brand-accent/20" />
          <section className="relative rounded-2xl bg-card/95 px-7 py-8 text-card-foreground shadow-[0_40px_100px_-30px_rgb(0_0_0/0.8)] backdrop-blur-xl dark:bg-card/85">
            {/* A brand logo is drawn for white, so in dark mode it sits on a white letterhead plaque. */}
            <header
              className={cn(
                'mb-7 flex flex-col items-center gap-3 text-center',
                assets.login_logo && '-mx-7 -mt-8 rounded-t-2xl px-7 pt-7 pb-6 dark:bg-white dark:shadow-[inset_0_-1px_0_var(--brand-accent)] dark:text-[oklch(0.25_0.03_260)] dark:[&_p]:text-[oklch(0.45_0.03_260)]',
              )}
            >
              {assets.login_logo ? (
                <img src={assets.login_logo} alt="" className="h-32 w-auto" />
              ) : (
                <BrandIcon className="size-12 rounded-xl" />
              )}
              <div className="space-y-1">
                <h2 className={assets.login_logo ? 'sr-only' : 'font-display text-3xl'}>Welcome back</h2>
                {assets.login_logo && tagline ? (
                  <p className="mx-auto max-w-[30ch] text-sm leading-relaxed text-balance text-muted-foreground">{tagline}</p>
                ) : (
                  <p className="text-sm text-muted-foreground">Sign in to {name}</p>
                )}
              </div>
            </header>

          <form onSubmit={submit} className="grid gap-4">
            <div className="grid gap-2">
              <Label htmlFor="email">Email</Label>
              <div className="relative">
                <LuMail aria-hidden className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  id="email"
                  name="email"
                  type="email"
                  autoComplete="username"
                  placeholder="you@example.com"
                  autoFocus
                  required
                  aria-invalid={invalid}
                  aria-describedby={error ? 'login-error' : undefined}
                  className="h-10 pl-9"
                />
              </div>
            </div>
            <div className="grid gap-2">
              <Label htmlFor="password">Password</Label>
              <div className="relative">
                <LuLock aria-hidden className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  id="password"
                  name="password"
                  type={showPw ? 'text' : 'password'}
                  autoComplete="current-password"
                  required
                  aria-invalid={invalid}
                  aria-describedby={error ? 'login-error' : undefined}
                  className="h-10 pr-10 pl-9"
                />
                <button
                  type="button"
                  onClick={() => setShowPw((v) => !v)}
                  aria-label={showPw ? 'Hide password' : 'Show password'}
                  aria-pressed={showPw}
                  className="absolute top-1/2 right-1 flex size-8 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50"
                >
                  {showPw ? <LuEyeOff className="size-4" /> : <LuEye className="size-4" />}
                </button>
              </div>
            </div>
            {error && (
              <p
                id="login-error"
                role="alert"
                className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive"
              >
                {error}
              </p>
            )}
            <Button type="submit" size="lg" disabled={login.isPending} className="group mt-1 w-full font-semibold">
              {login.isPending && <LuLoaderCircle className="animate-spin" />}
              {login.isPending ? 'Signing in…' : 'Sign in'}
            </Button>
          </form>
          </section>
        </div>
      </div>
    </main>
  )
}

/** The panel prefers login-bg.webp; browsers without WebP fall back to its .jpg sibling. */
const jpg = (url: string) => url.replace(/\.webp(?=\?|$)/, '.jpg')

function Sources({ url, media }: { url: string; media?: string }) {
  return (
    <>
      {/\.webp(\?|$)/.test(url) && <source type="image/webp" srcSet={url} media={media} />}
      <source srcSet={jpg(url)} media={media} />
    </>
  )
}
