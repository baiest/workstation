import { describe, expect, it } from 'vitest'
import { endsWithQuestion, needsYou, sessionAttention, worktreeSessionAttention, type Attention } from './attention'
import type { Session } from './types'

const now = new Date('2026-10-02T12:00:00Z')
const ago = (h: number) => new Date(now.getTime() - h * 3600_000).toISOString()
const s = (over: Partial<Session> = {}): Session => ({
  id: 's', source: 'cli', cwd: '/w', status: 'idle', state: 'waiting', lastActivity: ago(0.2), resumable: true, hasPlan: false, ...over,
})

describe('endsWithQuestion', () => {
  it.each([
    ['Which one do you prefer?', true],
    ['Shall I go on? ', true],
    ['Shall I "go on?"', true],
    ['Done.', false],
    ['', false],
    [undefined, false],
  ])('%s -> %s', (text, want) => expect(endsWithQuestion(text)).toBe(want))
})

describe('sessionAttention', () => {
  it('a fresh waiting session asks you with a question, otherwise it is output to review', () => {
    expect(sessionAttention(s({ lastMessage: 'Which file?' }), now)).toBe('asks-you')
    expect(sessionAttention(s({ lastMessage: 'All done.' }), now)).toBe('review-output')
    expect(sessionAttention(s(), now)).toBe('review-output')
  })

  it('a waiting or approval session older than the limit is stale', () => {
    expect(sessionAttention(s({ lastActivity: ago(3) }), now)).toBe('stale')
    expect(sessionAttention(s({ state: 'needs-approval', lastActivity: ago(3) }), now)).toBe('stale')
    expect(sessionAttention(s({ state: 'needs-approval' }), now)).toBe('approval')
  })

  it('a stopped session never waits for you', () => {
    expect(sessionAttention(s({ status: 'stopped' }), now)).toBe('none')
  })

  it('a failure counts for a day, and only for the newest session', () => {
    expect(sessionAttention(s({ state: 'failed', lastActivity: ago(1) }), now)).toBe('failed')
    expect(sessionAttention(s({ state: 'failed', lastActivity: ago(30) }), now)).toBe('none')
    expect(sessionAttention(s({ state: 'failed', lastActivity: ago(1) }), now, false)).toBe('none')
  })

  it('thinking and running a tool are working; finished is nothing', () => {
    expect(sessionAttention(s({ state: 'thinking', status: 'working' }), now)).toBe('working')
    expect(sessionAttention(s({ state: 'running-tool', status: 'working' }), now)).toBe('working')
    expect(sessionAttention(s({ state: 'finished', status: 'stopped' }), now)).toBe('none')
    expect(sessionAttention(s({ state: undefined, status: 'working' }), now)).toBe('working')
  })
})

describe('worktreeSessionAttention', () => {
  it('takes the most urgent of the sessions', () => {
    const w = { sessions: [s({ state: 'thinking', status: 'working' }), s({ id: 'b', lastMessage: 'Ready?' })] }
    expect(worktreeSessionAttention(w, now)).toBe('asks-you')
  })

  it('is none without sessions, and needsYou separates the kinds', () => {
    expect(worktreeSessionAttention({ sessions: [] }, now)).toBe('none')
    const calling: Attention[] = ['asks-you', 'approval', 'failed', 'review-output']
    const quiet: Attention[] = ['working', 'stale', 'none']
    expect(calling.every(needsYou)).toBe(true)
    expect(quiet.some(needsYou)).toBe(false)
  })
})
