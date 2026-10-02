// Zoom and UI-size helpers (pure, no DOM).

export const ZOOM_MIN = 0.3
export const ZOOM_MAX = 2
export const ZOOM_STEP = 0.1

const FONT_MIN = 14
const FONT_MAX = 28
const FONT_STEP = 2

/** Clamps a zoom factor to the supported range, rounded to 2 decimals. */
export function clampZoom(z: number): number {
  return Math.round(Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, z)) * 100) / 100
}

/** Zoom that makes `content` px fit `available` px of width; 100% when either is unknown. */
export function fitZoom(available: number, content: number): number {
  if (available <= 0 || content <= 0) return 1
  return clampZoom(available / content)
}

export const FONT_DEFAULT = 18

/** Keeps a root font size (px) in the readable range; garbage becomes the default. */
export function clampFontSize(px: number): number {
  if (!Number.isFinite(px)) return FONT_DEFAULT
  return Math.min(FONT_MAX, Math.max(FONT_MIN, px))
}

/** Root font size (px) after one step up (+1) or down (-1), kept readable. */
export function nextFontSize(current: number, dir: 1 | -1): number {
  return clampFontSize(current + dir * FONT_STEP)
}
