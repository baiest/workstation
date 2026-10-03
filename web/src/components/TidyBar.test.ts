import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import TidyBar from './TidyBar.vue'

const bar = (counts = { removable: 2, dormant: 3 }, branches?: { safe: number; risky: number }) =>
  mount(TidyBar, { props: { counts, branches } })

describe('TidyBar', () => {
  it('says what can be tidied', () => {
    expect(bar().text()).toContain('2 worktrees merged · 3 dormant')
  })

  it('renders nothing when all is tidy', () => {
    expect(bar({ removable: 0, dormant: 0 }, { safe: 0, risky: 2 }).find('[data-tidy]').exists()).toBe(false)
  })

  it('offers the cleanups that apply and emits them', async () => {
    const w = bar({ removable: 1, dormant: 0 }, { safe: 4, risky: 1 })
    await w.get('[data-tidy-worktrees]').trigger('click')
    await w.get('[data-tidy-branches]').trigger('click')
    expect(w.emitted('worktrees')).toHaveLength(1)
    expect(w.emitted('branches')).toHaveLength(1)
  })

  it('hides the worktree button when none is removable', () => {
    expect(bar({ removable: 0, dormant: 5 }).find('[data-tidy-worktrees]').exists()).toBe(false)
  })

  it('mentions the riskier branches without counting them as safe', () => {
    expect(bar({ removable: 1, dormant: 0 }, { safe: 0, risky: 3 }).get('[data-tidy]').attributes('title')).toContain('3')
  })
})
