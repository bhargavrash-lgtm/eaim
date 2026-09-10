import type { Config } from 'tailwindcss'
import colors from 'tailwindcss/colors'
import { brand } from './src/branding/theme.generated'

// brand's hex values come from scripts/generate-theme.mjs (see that
// file's header) -- extracted from the real logo, not hand-picked here.
//
// status/2xs/drawer added by the UI-consistency-audit token brief: these
// close real, found drift (a 3x-duplicated status-badge palette with a
// 700-vs-800 text-shade inconsistency; a 9/10/11px arbitrary-value
// micro-text split; 3 independently hand-picked slide-out-drawer widths)
// rather than being speculative additions. status.* is derived directly
// from Tailwind's own default green/amber/red -- not hand-typed hex --
// so it is byte-identical to what every existing green/amber/red badge
// already renders today.
const config: Config = {
  content: [
    './index.html',
    './src/**/*.{js,ts,jsx,tsx}',
  ],
  theme: {
    extend: {
      colors: {
        brand,
        status: {
          success: { DEFAULT: colors.green[100], text: colors.green[800] },
          warning: { DEFAULT: colors.amber[100], text: colors.amber[800] },
          danger: { DEFAULT: colors.red[100], text: colors.red[800] },
        },
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
