import type { Config } from 'tailwindcss'
import { brand } from './src/branding/theme.generated'

// brand's hex values come from scripts/generate-theme.mjs (see that
// file's header) -- extracted from the real logo, not hand-picked here.
const config: Config = {
  content: [
    './index.html',
    './src/**/*.{js,ts,jsx,tsx}',
  ],
  theme: {
    extend: {
      colors: {
        brand,
      },
    },
  },
  plugins: [],
}

export default config
