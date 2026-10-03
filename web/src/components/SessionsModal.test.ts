import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import SessionsModal from './SessionsModal.vue'
import type { Session } from '../types'

const now = new Date('2026-10-02T12:00:00Z')

const s = (n: number, over: Partial<Session> = {}): Session => ({
  id: `id-${n}`, source: 'cli', cwd: '/w', status: 'stopped', state: 'finished',
  prompt: `Prompt number ${n}`, lastMessage: `Last words of ${n}`,
  lastActivity: `2026-10-0${1 + (n % 2)}T0${n % 9}:00:00Z`, resumable: true, hasPlan: false, ...over,
})

const many = (count: number) => Array.from({ length: count }, (_, i) => s(i + 1))

const render = (sessions: Session[], title = 'REG-5393-ace-sync') =>
  mount(SessionsModal, { props: { sessions, title, now } })

describe('SessionsModal', () => {
  it('lists every session of the worktree, however many there are', () => {
    const w = render(many(57))
    expect(w.findAll('[data-session]')).toHaveLength(57)
    expect(w.text()).toContain('57 sessions')
    expect(w.text()).toContain('REG-5393-ace-sync')
  })

  it('tells chats apart by what was asked, with the last words under it', () => {
    const w = render([s(1, { title: 'Titled one' }), s(2)])
    const first = w.get('[data-session="id-1"]')
    expect(first.get('.s-title').text()).toBe('Titled one')
    expect(w.get('[data-session="id-2"] .s-title').text()).toBe('Prompt number 2')
    expect(w.get('[data-session="id-2"] .s-last').text()).toContain('Last words of 2')
  })

  it('shows what each one is doing, so the one waiting for you stands out', () => {
    const w = render([s(1, { state: 'waiting' }), s(2, { state: 'failed' }), s(3, { state: 'thinking' })])
    expect(w.get('[data-session="id-1"] .s-state').text()).toBe('Waiting for you')
    expect(w.get('[data-session="id-1"]').classes()).toContain('tone-attention')
    expect(w.get('[data-session="id-2"]').classes()).toContain('tone-danger')
    expect(w.get('[data-session="id-3"] .s-state').text()).toBe('Thinking')
  })

  it('says where each session lives', () => {
    const w = render([s(1), s(2, { source: 'desktop', desktopId: 'local_x' })])
    expect(w.get('[data-session="id-1"] .s-source').text()).toBe('CLI')
    expect(w.get('[data-session="id-2"] .s-source').text()).toBe('Desktop')
  })

  it('searches by what was asked, the title, the last words or the id', async () => {
    const w = render(many(30))
    await w.get('[data-search]').setValue('number 17')
    expect(w.findAll('[data-session]').map((x) => x.attributes('data-session'))).toEqual(['id-17'])
    expect(w.text()).toContain('1 of 30')

    await w.get('[data-search]').setValue('LAST WORDS OF 2')
    expect(w.findAll('[data-session]').length).toBeGreaterThan(1) // 2, 20..29
    await w.get('[data-search]').setValue('id-5')
    expect(w.find('[data-session="id-5"]').exists()).toBe(true)
    await w.get('[data-search]').setValue('zzz')
    expect(w.text()).toContain('No session matches')
    await w.get('[data-search]').setValue('')
    expect(w.findAll('[data-session]')).toHaveLength(30)
  })

  it('resumes and opens the plan of the chosen session', async () => {
    const w = render([s(1), s(2, { hasPlan: true })])
    await w.get('[data-session="id-2"] [data-action=resume]').trigger('click')
    await w.get('[data-session="id-2"] [data-action=plan]').trigger('click')
    expect(w.emitted('resume')![0]).toEqual(['id-2'])
    expect(w.emitted('plan')![0]).toEqual(['id-2'])
  })

  it('offers resume only where it can work, and a plan only where there is one', () => {
    const w = render([s(1, { resumable: false }), s(2)])
    expect(w.find('[data-session="id-1"] [data-action=resume]').exists()).toBe(false)
    expect(w.find('[data-session="id-2"] [data-action=resume]').exists()).toBe(true)
    expect(w.find('[data-session="id-2"] [data-action=plan]').exists()).toBe(false)
  })

  it('says when there is nothing', () => {
    expect(render([]).text()).toContain('No sessions')
  })

  it('closes with Escape and the button', async () => {
    const w = render([s(1)])
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await w.get('[data-close]').trigger('click')
    expect(w.emitted('close')).toHaveLength(2)
  })
})
