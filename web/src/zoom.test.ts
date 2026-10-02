import { describe, expect, it } from 'vitest'
import { ZOOM_MAX, ZOOM_MIN, clampFontSize, clampZoom, fitZoom, nextFontSize } from './zoom'

describe('clampFontSize', () => {
  it('keeps a stored size inside the readable range', () => {
    expect(clampFontSize(18)).toBe(18)
    expect(clampFontSize(28)).toBe(28)
    expect(clampFontSize(14)).toBe(14)
    expect(clampFontSize(5)).toBe(14)
    expect(clampFontSize(400)).toBe(28)
  })

  it('falls back to the default for garbage', () => {
    expect(clampFontSize(NaN)).toBe(18)
    expect(clampFontSize(Infinity)).toBe(18)
  })
})

describe('clampZoom', () => {
  it('keeps zoom within limits and rounds to 2 decimals', () => {
    expect(clampZoom(1)).toBe(1)
    expect(clampZoom(0.05)).toBe(ZOOM_MIN)
    expect(clampZoom(9)).toBe(ZOOM_MAX)
    expect(clampZoom(1.1000000000000001)).toBe(1.1)
    expect(clampZoom(0.1 + 0.2 + 0.7)).toBe(1)
  })
})

describe('fitZoom', () => {
  it('scales the content to the available width', () => {
    expect(fitZoom(800, 1000)).toBe(0.8)
    expect(fitZoom(500, 1000)).toBe(0.5)
  })

  it('never goes below the minimum, and may enlarge a small graph a little', () => {
    expect(fitZoom(100, 1000)).toBe(ZOOM_MIN)
    expect(fitZoom(5000, 100)).toBe(ZOOM_MAX)
  })

  it('falls back to 100% when sizes are unknown', () => {
    expect(fitZoom(0, 1000)).toBe(1)
    expect(fitZoom(800, 0)).toBe(1)
  })
})

describe('nextFontSize', () => {
  it('steps the UI size and stays readable', () => {
    expect(nextFontSize(18, 1)).toBe(20)
    expect(nextFontSize(18, -1)).toBe(16)
    expect(nextFontSize(28, 1)).toBe(28)
    expect(nextFontSize(14, -1)).toBe(14)
  })
})
