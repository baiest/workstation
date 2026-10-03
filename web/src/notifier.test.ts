import { describe, expect, it, vi } from 'vitest'
import { deliver, tabTitle } from './notifier'
import type { NotifyEvent } from './notify'

const ev: NotifyEvent = { id: 'a', kind: 'waiting', title: 'Claude is waiting: LOY-96', body: 'chat a' }

describe('deliver', () => {
  it('shows one notification per event when enabled, granted and the page is hidden', () => {
    const make = vi.fn()
    deliver([ev], { enabled: true, permission: 'granted', hidden: true, make })
    expect(make).toHaveBeenCalledWith('Claude is waiting: LOY-96', { body: 'chat a', tag: 'a' })
  })

  it.each([
    ['off', { enabled: false, permission: 'granted', hidden: true }],
    ['not granted', { enabled: true, permission: 'denied', hidden: true }],
    ['page visible', { enabled: true, permission: 'granted', hidden: false }],
  ])('stays silent when %s', (_, st) => {
    const make = vi.fn()
    deliver([ev], { ...st, make })
    expect(make).not.toHaveBeenCalled()
  })

  it('caps a burst, so a returning laptop does not spam', () => {
    const make = vi.fn()
    deliver(Array.from({ length: 12 }, (_, i) => ({ ...ev, id: String(i) })), { enabled: true, permission: 'granted', hidden: true, make })
    expect(make).toHaveBeenCalledTimes(5)
  })
})

describe('tabTitle', () => {
  it('shows how many sessions wait for you', () => {
    expect(tabTitle(0)).toBe('workstation')
    expect(tabTitle(2)).toBe('(2) workstation')
  })
})
