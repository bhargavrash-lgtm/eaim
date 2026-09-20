import type { Config } from 'tailwindcss'

// REPAINTED (task brief, 2026-09-20): brand.* and status.* below are now
// hand-authored from DESIGN_SYSTEM.md's Admin/Workspace palette (source of
// truth: https://claude.ai/artifact/4j2DqNrWQbHRxTZD3KAJYf, verified
// directly against the canvas before this change), REPLACING their prior
// values -- not an extension. This is a deliberate divergence from
// `src/branding/theme.generated.ts` (still real, still logo-extracted,
// still do-not-hand-edit, left untouched): that file continues to
// describe the actual rheoARC logo image's own colors (read by
// `Logo.tsx`'s dormant `variant="mark"` monogram); `brand.*` here now
// describes the product's UI accent instead. The two are intentionally
// no longer the same values -- see BUILT.md's eami-ui section for why.
// brand-500 is DESIGN_SYSTEM.md's Accent hex, unmodified; every other
// step is derived via the same OKLCH method theme.generated.ts's own
// header documents (hold hue+chroma, vary lightness, taper chroma near
// the extremes) -- not hand-picked, and not re-extracted from any image.
// brand-800 is a genuinely new step: the prior ramp never defined one,
// which made every `text-brand-800` call site (SettingsPage.tsx) a
// silent no-op class this whole time -- fixed as a side effect of
// rebuilding the ramp, not a separate task.
//
// status.* moves from B-176/177's Tailwind-stock green/amber/red-100/800
// to DESIGN_SYSTEM.md's exact Success/Warning/Danger bg/text pairs,
// independently confirmed byte-exact against every real pill/badge in
// the canvas (StatusPill/RiskPill/ApprovalsPage/AuditPage/PoliciesPage
// all consume this shape unchanged -- zero component edits needed).
//
// ink.* is new: DESIGN_SYSTEM.md's primary text color (#1A2140) plus its
// two documented muted-text steps (secondary body vs. lighter caption/id
// text). Currently unused by any component (nothing today calls
// `text-ink`/`text-ink-muted`/`text-ink-faint`) -- added so a future page
// brief has these tokens to build against rather than inventing new
// values ad hoc, same reasoning as fontFamily/boxShadow below. The
// doc's second, dark-surface Ink value (#0B1130) is deliberately NOT
// added -- this app has no dark mode today, so it has nowhere to attach.
//
// fontSize/spacing.drawer: unchanged, from the earlier UI-consistency
// token brief (B-176/177) -- real, in-use, not part of this repaint.
//
// fontFamily and boxShadow are genuinely NEW additions (task brief),
// not replacements of anything: no prior fontFamily/boxShadow extension
// existed. fontFamily cascades automatically everywhere via Tailwind's
// own Preflight `html { font-family }` base rule and the 13 existing
// `font-mono` call sites -- no component edit needed, but it also needs
// the actual font FILE loaded (index.html's new Google Fonts <link>,
// same commit) or it silently falls through the stack and renders
// nothing different. boxShadow's 3 elevation levels are copied verbatim
// from DESIGN_SYSTEM.md §4 -- shipped here, but genuinely inert: zero
// components reference `shadow-l1`/`shadow-l2`/`shadow-l3` yet, and
// wiring one in is a component-logic change this brief's scope
// explicitly excludes. Do not read their presence here as evidence any
// page's elevation actually changed -- it hasn't, yet.
const config: Config = {
  content: [
    './index.html',
    './src/**/*.{js,ts,jsx,tsx}',
  ],
  theme: {
    extend: {
      colors: {
        brand: {
          50: '#e1f4ff',
          100: '#cee7ff',
          500: '#3B5BDB',
          600: '#2a44c3',
          700: '#1c2cab',
          800: '#11178c',
          900: '#0a006f',
        },
        status: {
          success: { DEFAULT: '#E8F5EE', text: '#1A7A4C' },
          warning: { DEFAULT: '#FBF1DD', text: '#8A6512' },
          danger: { DEFAULT: '#FBEAEA', text: '#8A2E2E' },
        },
        ink: {
          DEFAULT: '#1A2140',
          muted: '#6B7290',
          faint: '#8890AD',
        },
      },
      fontFamily: {
        sans: ['"IBM Plex Sans"', 'ui-sans-serif', 'system-ui', 'sans-serif'],
        mono: ['"IBM Plex Mono"', 'ui-monospace', 'monospace'],
      },
      boxShadow: {
        // DESIGN_SYSTEM.md §4, verbatim -- L1 resting card/panel, L2
        // persistent chrome (top app bar), L3 selected/focused element.
        // L1/L3 also specify a paired border (not a shadow property) --
        // that stays in each future consuming component's own className,
        // same as the doc itself documents it.
        l1: '0 1px 2px rgba(26,33,64,0.04), 0 2px 6px rgba(26,33,64,0.06)',
        l2: '0 2px 10px rgba(26,33,64,0.07)',
        l3: '0 4px 16px rgba(59,91,219,0.20), 0 2px 6px rgba(26,33,64,0.08)',
      },
      fontSize: {
        // The most common of the 9/10/11px arbitrary values found in use
        // (6 of 11 instances) -- defines the token; does not itself
        // migrate those existing call sites (separate follow-up).
        '2xs': ['10px', { lineHeight: '1rem' }],
      },
      spacing: {
        // The widest of the 3 found slide-out-drawer widths (480/440/420px).
        // Only the 440/420 panels adopt this token in this brief;
        // AgentsPage's w-96 (384px, a stock token, a 4th distinct width)
        // is deliberately left alone -- a real width change of that size
        // belongs to the future SlideOverPanel brief, not this one.
        drawer: '30rem',
      },
    },
  },
  plugins: [],
}

export default config
