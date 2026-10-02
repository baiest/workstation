import { describe, expect, it } from 'vitest'
import { loadHidden, saveHidden, toggleHidden, type Store } from './hidden'

const memory = (initial?: string): Store & { data: Map<string, string> } => {
  const data = new Map<string, string>()
  if (initial !== undefined) data.set('workstation.hidden-repos', initial)
  return { data, getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v) }
}

const broken: Store = {
  getItem: () => {
    throw new Error('storage blocked')
  },
  setItem: () => {
    throw new Error('storage blocked')
  },
}

describe('loadHidden', () => {
  it('reads the saved list', () => {
    expect(loadHidden(memory('["/a","/b"]'))).toEqual(['/a', '/b'])
  })

  it('starts empty when nothing was saved', () => {
    expect(loadHidden(memory())).toEqual([])
  })

  it.each(['{nope', '"just a string"', '{"a":1}', 'null', '42'])('ignores corrupt data %s', (raw) => {
    expect(loadHidden(memory(raw))).toEqual([])
  })

  it('keeps only strings, without duplicates', () => {
    expect(loadHidden(memory('["/a", 3, null, "/a", "/b"]'))).toEqual(['/a', '/b'])
  })

  it('survives unavailable storage', () => {
    expect(loadHidden(broken)).toEqual([])
    expect(loadHidden(undefined)).toEqual([])
  })
})

describe('saveHidden', () => {
  it('writes JSON that loadHidden reads back', () => {
    const store = memory()
    saveHidden(store, ['/a', '/b'])
    expect(loadHidden(store)).toEqual(['/a', '/b'])
  })

  it('never throws when storage is unavailable', () => {
    expect(() => saveHidden(broken, ['/a'])).not.toThrow()
    expect(() => saveHidden(undefined, ['/a'])).not.toThrow()
  })
})

describe('toggleHidden', () => {
  it('hides a visible project and shows a hidden one, without mutating the input', () => {
    const list = ['/a']
    expect(toggleHidden(list, '/b')).toEqual(['/a', '/b'])
    expect(toggleHidden(list, '/a')).toEqual([])
    expect(list).toEqual(['/a'])
  })
})
