#!/usr/bin/env node
/**
 * Generates src/branding/theme.generated.ts and public/favicon.svg from the
 * source logo, by extracting its real dominant colors (colorthief, MMCQ
 * quantization) and building a perceptually-uniform (OKLCH) tint/shade ramp
 * anchored to the exact extracted color -- not a hand-picked palette.
 *
 * Run: npm run generate-branding-theme
 * Rerun whenever src/branding/source/ logo changes. theme.generated.ts and
 * public/favicon.svg are generated artifacts -- do not hand-edit them.
 */
import { getSwatches } from 'colorthief'
import sharp from 'sharp'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import fs from 'node:fs'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const ROOT = path.resolve(__dirname, '..')
const REPO_ROOT = path.resolve(ROOT, '..')

const RAW_LOGO = path.join(REPO_ROOT, 'Logo', 'rheo logo name.png')
const TRIMMED_LOGO = path.join(ROOT, 'src', 'branding', 'source', 'rheoarc-logo.png')
const THEME_OUT = path.join(ROOT, 'src', 'branding', 'theme.generated.ts')
const FAVICON_OUT = path.join(ROOT, 'public', 'favicon.svg')

// ---- OKLCH <-> sRGB (Bjorn Ottosson's OKLab model: https://bottosson.github.io/posts/oklab/) ----
function srgbToLinear(c) {
  c /= 255
  return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
}
function linearToSrgb(c) {
  const v = c <= 0.0031308 ? c * 12.92 : 1.055 * Math.pow(c, 1 / 2.4) - 0.055
  return Math.round(Math.min(1, Math.max(0, v)) * 255)
}
function rgbToOklab(r, g, b) {
  const lr = srgbToLinear(r)
  const lg = srgbToLinear(g)
  const lb = srgbToLinear(b)
  const l = 0.4122214708 * lr + 0.5363325363 * lg + 0.0514459929 * lb
  const m = 0.2119034982 * lr + 0.6806995451 * lg + 0.1073969566 * lb
  const s = 0.0883024619 * lr + 0.2817188376 * lg + 0.6299787005 * lb
  const l_ = Math.cbrt(l)
  const m_ = Math.cbrt(m)
  const s_ = Math.cbrt(s)
  return {
    L: 0.2104542553 * l_ + 0.793617785 * m_ - 0.0040720468 * s_,
    A: 1.9779984951 * l_ - 2.428592205 * m_ + 0.4505937099 * s_,
    B: 0.0259040371 * l_ + 0.7827717662 * m_ - 0.808675766 * s_,
  }
}
function oklabToRgb(L, A, B) {
  const l_ = L + 0.3963377774 * A + 0.2158037573 * B
  const m_ = L - 0.1055613458 * A - 0.0638541728 * B
  const s_ = L - 0.0894841775 * A - 1.291485548 * B
  const l = l_ ** 3
  const m = m_ ** 3
  const s = s_ ** 3
  const r = 4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s
  const g = -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s
  const b = -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s
  return { r: linearToSrgb(r), g: linearToSrgb(g), b: linearToSrgb(b) }
}
function oklab2oklch(L, A, B) {
  return { L, C: Math.sqrt(A * A + B * B), H: Math.atan2(B, A) }
}
function oklch2oklab(L, C, H) {
  return { L, A: C * Math.cos(H), B: C * Math.sin(H) }
}
function hexToRgb(hex) {
  const n = parseInt(hex.replace('#', ''), 16)
  return { r: (n >> 16) & 255, g: (n >> 8) & 255, b: n & 255 }
}
function rgbToHex({ r, g, b }) {
  return '#' + [r, g, b].map((v) => v.toString(16).padStart(2, '0')).join('')
}
function oklchOf(hex) {
  const { r, g, b } = hexToRgb(hex)
  const { L, A, B } = rgbToOklab(r, g, b)
  const { C, H } = oklab2oklch(L, A, B)
  return { L, C, H }
}

// One ramp step: same hue+chroma as the anchor, lightness overridden.
// Chroma is tapered down near the extremes -- a straight lightness-only
// ramp pushes very light/dark stops out of the sRGB gamut and clips.
function ramp(anchorHex, targetL, chromaScale = 1) {
  const { r, g, b } = hexToRgb(anchorHex)
  const { L, A, B } = rgbToOklab(r, g, b)
  const { C, H } = oklab2oklch(L, A, B)
  const { A: A2, B: B2 } = oklch2oklab(targetL, C * chromaScale, H)
  return rgbToHex(oklabToRgb(targetL, A2, B2))
}

async function main() {
  fs.mkdirSync(path.dirname(TRIMMED_LOGO), { recursive: true })
  fs.mkdirSync(path.dirname(FAVICON_OUT), { recursive: true })

  // The raw asset is a 2816x1536 PNG that's ~90% blank canvas around a
  // small wordmark. Trim the uniform margin and shrink to a sane web size.
  const trimmedBuffer = await sharp(RAW_LOGO).trim().toBuffer()
  await sharp(trimmedBuffer)
    .resize({ width: 900, withoutEnlargement: true })
    .png({ compressionLevel: 9 })
    .toFile(TRIMMED_LOGO)

  // Extract the logo's real colors. ignoreWhite skips the background;
  // colorCount high enough that "rheo" (teal) and "ARC" (navy) come back
  // as distinct swatches instead of averaging into one muddy color.
  const swatches = await getSwatches(TRIMMED_LOGO, { ignoreWhite: true, colorCount: 8 })

  const found = Object.entries(swatches)
    .filter(([, v]) => v)
    .map(([name, v]) => ({ name, hex: v.color.hex(), ...oklchOf(v.color.hex()) }))
  if (found.length === 0) {
    throw new Error('Color extraction failed: colorthief returned no usable swatch for the logo')
  }

  // Darkest real swatch = the bold navy "ARC" -- anchors brand-500.
  const byLightness = [...found].sort((a, b) => a.L - b.L)
  const navy = byLightness[0]

  // Most-saturated swatch other than the navy anchor itself = the teal
  // "rheo" ink. Picking by chroma (not by lightness) matters here: PNG
  // anti-aliasing along the glyph edges produces pale, low-chroma
  // near-white swatches that are "lighter" than the real teal fill but
  // aren't a genuine second logo color -- sorting by lightness alone
  // would grab one of those instead of the actual ink.
  const teal = [...found].filter((s) => s.hex !== navy.hex).sort((a, b) => b.C - a.C)[0] ?? navy

  const brand = {
    50: ramp(navy.hex, 0.97, 0.35),
    100: ramp(navy.hex, 0.93, 0.45),
    500: navy.hex, // pinned to the exact extracted color, not recomputed
    600: ramp(navy.hex, Math.max(0.05, navy.L - 0.07), 1),
    700: ramp(navy.hex, Math.max(0.05, navy.L - 0.14), 1),
    900: ramp(navy.hex, Math.max(0.05, navy.L - 0.28), 0.85),
  }

  const generatedAt = new Date().toISOString()
  const header = `/**
 * AUTO-GENERATED by scripts/generate-theme.mjs -- do not hand-edit.
 * Regenerate with: npm run generate-branding-theme
 *
 * Source logo: ${path.relative(REPO_ROOT, RAW_LOGO).replace(/\\/g, '/')}
 * Generated:   ${generatedAt}
 *
 * Extraction: colorthief.getSwatches() (MMCQ quantization, ignoreWhite:true)
 * against the trimmed logo found ${found.length} real swatch(es). The
 * lowest-lightness swatch is the bold navy "ARC" glyph (anchors brand-500
 * below); the highest-chroma swatch OTHER than that navy anchor is the
 * teal "rheo" glyph (kept as a reference below, not wired into the ramp) --
 * chroma, not lightness, picks it out because PNG anti-aliasing along the
 * glyph edges produces pale near-white swatches that are lighter than the
 * real teal fill but aren't a genuine second logo color.
 * The 50/100/600/700/900 steps are NOT separately sampled colors -- each is
 * computed from the navy anchor by holding its OKLCH hue+chroma constant
 * and moving only lightness (tapering chroma near the extremes to stay in
 * sRGB gamut), per Bjorn Ottosson's OKLab model. brand-500 is the exact
 * extracted hex, unmodified.
 */
`

  const themeTs = `${header}
export const rawSwatches = {
  navy: ${JSON.stringify(navy.hex)},
  teal: ${JSON.stringify(teal.hex)},
} as const

export const brand = ${JSON.stringify(brand, null, 2)} as const
`
  fs.writeFileSync(THEME_OUT, themeTs)

  // Favicon: an SVG can't import a TS module, so its colors are inlined
  // literally -- but written by this same script, from the same
  // extraction, in the same run, so it can't drift out of sync with
  // theme.generated.ts. Placeholder glyph until a real icon-only mark
  // exists (the source logo is a wordmark only, see BUILT.md).
  const favicon = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
  <rect width="32" height="32" rx="7" fill="${brand[600]}"/>
  <text x="16" y="23" font-family="ui-sans-serif, system-ui, sans-serif" font-size="19" font-weight="700" fill="${brand[50]}" text-anchor="middle">r</text>
</svg>
`
  fs.writeFileSync(FAVICON_OUT, favicon)

  console.log('Extracted swatches:', found)
  console.log('Anchor (brand-500):', navy.hex)
  console.log('Generated brand ramp:', brand)
  console.log('Wrote:', path.relative(REPO_ROOT, THEME_OUT).replace(/\\/g, '/'))
  console.log('Wrote:', path.relative(REPO_ROOT, TRIMMED_LOGO).replace(/\\/g, '/'))
  console.log('Wrote:', path.relative(REPO_ROOT, FAVICON_OUT).replace(/\\/g, '/'))
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
