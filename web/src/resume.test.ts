import { describe, expect, it } from 'vitest'
import { resumeDecision } from './resume'
import type { Session } from './types'

const s = (over: Partial<Session> = {}): Session => ({
  id: 'abc', source: 'cli', cwd: '/Users/me/repo', status: 'stopped', lastActivity: '2026-10-01T00:00:00Z', resumable: true, hasPlan: false, ...over,
})

describe('resumeDecision', () => {
  it('uses the CLI when that is the preference', () => {
    expect(resumeDecision(s({ desktopId: 'local_1' }), 'cli')).toEqual({ mode: 'cli' })
  })

  it('opens Desktop for a session that exists there, and says which one to pick', () => {
    const d = resumeDecision(s({ desktopId: 'local_1', title: 'Add checks', cwd: '/w/repo' }), 'desktop')
    expect(d.mode).toBe('desktop')
    expect(d.note).toContain('Add checks')
    expect(d.note).toContain('/w/repo')
  })

  it('names the folder when the Desktop session has no title', () => {
    const d = resumeDecision(s({ desktopId: 'local_1', title: '' }), 'desktop')
    expect(d.note).toContain('/Users/me/repo')
  })

  it('falls back to the CLI for a session that only exists in the CLI, and says so', () => {
    const d = resumeDecision(s(), 'desktop')
    expect(d.mode).toBe('cli')
    expect(d.note).toContain('CLI')
  })

  it('does not depend on a missing session', () => {
    expect(resumeDecision(undefined, 'desktop')).toEqual({ mode: 'cli' })
    expect(resumeDecision(undefined, 'cli')).toEqual({ mode: 'cli' })
  })
})
