import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import CleanupModal from './CleanupModal.vue'
import { fetchCleanup, runCleanup } from '../api'
import type { CleanupPreview } from '../types'

vi.mock('../api', () => ({ fetchCleanup: vi.fn(), runCleanup: vi.fn() }))

const preview = (over: Partial<CleanupPreview> = {}): CleanupPreview => ({
  days: 30,
  candidates: [
    { branch: 'old-one', sha: 'aaa1111111111111', prNumber: 11, prTitle: 'Fix thing', prUrl: 'https://x/11', mergedAt: '2026-08-01T00:00:00Z' },
    { branch: 'old-two', sha: 'bbb2222222222222', prNumber: 12, prTitle: 'Add other', mergedAt: '2026-07-01T00:00:00Z' },
  ],
  skipped: [{ branch: 'recent', prNumber: 13, reason: 'merged 5 days ago (needs 30 or more)' }],
  warnings: [],
  ...over,
})

const open = async (p: CleanupPreview = preview()) => {
  vi.mocked(fetchCleanup).mockResolvedValue(p)
  const w = mount(CleanupModal, { props: { repo: '/r', repoName: 'myrepo' } })
  await flushPromises()
  return w
}

beforeEach(() => {
  vi.mocked(fetchCleanup).mockReset()
  vi.mocked(runCleanup).mockReset()
})

describe('CleanupModal', () => {
  it('loads the preview for the repo and lists every candidate, ticked', async () => {
    const w = await open()
    expect(fetchCleanup).toHaveBeenCalledWith('/r', 30)
    expect(w.text()).toContain('myrepo')
    expect(w.text()).toContain('old-one')
    expect(w.text()).toContain('#11')
    expect(w.text()).toContain('Fix thing')
    expect(w.text()).toContain('aaa1111') // short sha
    const boxes = w.findAll('input[type=checkbox][data-branch]')
    expect(boxes).toHaveLength(2)
    expect(boxes.every((b) => (b.element as HTMLInputElement).checked)).toBe(true)
    expect(w.get('[data-delete]').text()).toContain('Delete 2 local branches')
  })

  describe('old branches without a PR', () => {
    const mixed = () =>
      preview({
        candidates: [
          { branch: 'pr-one', sha: 'aaa1111111111111', kind: 'pr-merged', prNumber: 11, prTitle: 'Fix thing', mergedAt: '2026-08-01T00:00:00Z' },
          { branch: 'merged-in-main', sha: 'bbb2222222222222', kind: 'stale-merged', lastCommit: '2026-07-01T00:00:00Z' },
          { branch: 'on-origin', sha: 'ccc3333333333333', kind: 'stale-on-remote', lastCommit: '2026-07-01T00:00:00Z', ahead: 3 },
          { branch: 'only-here', sha: 'ddd4444444444444', kind: 'stale-local-only', lastCommit: '2026-06-01T00:00:00Z', ahead: 4 },
        ],
        skipped: [],
      })

    it('groups the branches by how much deleting them could lose', async () => {
      const w = await open(mixed())
      const groups = w.findAll('[data-group]').map((g) => g.attributes('data-group'))
      expect(groups).toEqual(['pr-merged', 'stale-merged', 'stale-on-remote', 'stale-local-only'])
      expect(w.get('[data-group="stale-local-only"]').text()).toContain('only on this machine')
      expect(w.get('[data-group="stale-on-remote"]').text()).toContain('remote')
    })

    it('ticks the safe ones and leaves the risky ones for the user to choose', async () => {
      const w = await open(mixed())
      const checked = (b: string) => (w.get(`input[data-branch="${b}"]`).element as HTMLInputElement).checked
      expect(checked('pr-one')).toBe(true)
      expect(checked('merged-in-main')).toBe(true)
      expect(checked('on-origin')).toBe(false)
      expect(checked('only-here')).toBe(false)
      expect(w.get('[data-delete]').text()).toContain('Delete 2 local branches')
    })

    it('says how many commits exist nowhere else', async () => {
      const w = await open(mixed())
      expect(w.get('[data-branch-row="only-here"]').text()).toContain('4 commits exist only here')
    })

    it('"Select all" never ticks a branch that exists only here', async () => {
      const w = await open(mixed())
      await w.get('[data-select-all]').trigger('click')
      const checked = (b: string) => (w.get(`input[data-branch="${b}"]`).element as HTMLInputElement).checked
      expect(checked('on-origin')).toBe(true)
      expect(checked('only-here')).toBe(false)
    })

    it('needs an explicit confirmation before deleting a branch that exists only here', async () => {
      vi.mocked(runCleanup).mockResolvedValue([{ branch: 'only-here', sha: 'ddd4444444444444', deleted: true }])
      const w = await open(mixed())
      await w.get('[data-select-none]').trigger('click')
      expect(w.find('[data-ack]').exists()).toBe(false)

      await w.get('input[data-branch="only-here"]').setValue(true)
      expect(w.find('[data-ack]').exists()).toBe(true)
      expect(w.get('[data-delete]').attributes('disabled')).toBeDefined() // not until confirmed

      await w.get('[data-ack]').setValue(true)
      expect(w.get('[data-delete]').attributes('disabled')).toBeUndefined()
      await w.get('[data-delete]').trigger('click')
      await flushPromises()
      expect(runCleanup).toHaveBeenCalledWith('/r', 30, [{ branch: 'only-here', sha: 'ddd4444444444444' }], true)
    })

    it('does not send the confirmation when no such branch is selected', async () => {
      vi.mocked(runCleanup).mockResolvedValue([])
      const w = await open(mixed())
      await w.get('[data-delete]').trigger('click')
      await flushPromises()
      expect(vi.mocked(runCleanup).mock.calls[0][3]).toBe(false)
    })

    it('forgets the confirmation when the risky branch is unticked again', async () => {
      const w = await open(mixed())
      await w.get('input[data-branch="only-here"]').setValue(true)
      await w.get('[data-ack]').setValue(true)
      await w.get('input[data-branch="only-here"]').setValue(false)
      expect(w.find('[data-ack]').exists()).toBe(false)
      await w.get('input[data-branch="only-here"]').setValue(true)
      expect((w.get('[data-ack]').element as HTMLInputElement).checked).toBe(false)
    })
  })

  it('says what it will not touch', async () => {
    const w = await open()
    expect(w.text()).toContain('Remote branches are not touched')
    expect(w.text()).toContain('recent')
    expect(w.text()).toContain('merged 5 days ago')
  })

  it('updates the count as branches are unticked and disables delete at zero', async () => {
    const w = await open()
    await w.get('input[data-branch="old-one"]').setValue(false)
    expect(w.get('[data-delete]').text()).toContain('Delete 1 local branch')
    expect(w.get('[data-delete]').text()).not.toContain('branches')

    await w.get('input[data-branch="old-two"]').setValue(false)
    expect(w.get('[data-delete]').attributes('disabled')).toBeDefined()

    await w.get('[data-select-all]').trigger('click')
    expect(w.get('[data-delete]').text()).toContain('Delete 2 local branches')
    await w.get('[data-select-none]').trigger('click')
    expect(w.get('[data-delete]').attributes('disabled')).toBeDefined()
  })

  it('deletes only the ticked branches, with the sha that was shown', async () => {
    vi.mocked(runCleanup).mockResolvedValue([{ branch: 'old-two', sha: 'bbb2222222222222', deleted: true }])
    const w = await open()
    await w.get('input[data-branch="old-one"]').setValue(false)
    await w.get('[data-delete]').trigger('click')
    await flushPromises()

    expect(runCleanup).toHaveBeenCalledWith('/r', 30, [{ branch: 'old-two', sha: 'bbb2222222222222' }], false)
    expect(w.emitted('deleted')).toHaveLength(1)
  })

  it('shows what happened and how to restore each deleted branch', async () => {
    vi.mocked(runCleanup).mockResolvedValue([
      { branch: 'old-one', sha: 'aaa1111111111111', deleted: true },
      { branch: 'old-two', sha: 'bbb2222222222222', deleted: false, error: 'the preview is out of date' },
    ])
    const w = await open()
    await w.get('[data-delete]').trigger('click')
    await flushPromises()

    const text = w.text()
    expect(text).toContain('Deleted 1 of 2')
    expect(text).toContain('git branch old-one aaa1111111111111')
    expect(text).not.toContain('git branch old-two')
    expect(text).toContain('the preview is out of date')
    expect(w.find('[data-delete]').exists()).toBe(false)
  })

  it('shows an error if the request fails and keeps the list', async () => {
    vi.mocked(runCleanup).mockRejectedValue(new Error('internal error'))
    const w = await open()
    await w.get('[data-delete]').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('internal error')
    expect(w.findAll('input[type=checkbox][data-branch]')).toHaveLength(2)
  })

  it('does not delete twice while a request is in flight', async () => {
    let finish: (v: never[]) => void = () => {}
    vi.mocked(runCleanup).mockReturnValue(new Promise((r) => (finish = r as typeof finish)))
    const w = await open()
    await w.get('[data-delete]').trigger('click')
    await w.get('[data-delete]').trigger('click')
    expect(runCleanup).toHaveBeenCalledTimes(1)
    finish([])
    await flushPromises()
  })

  it('has an empty state and surfaces warnings', async () => {
    const w = await open(preview({ candidates: [], skipped: [], warnings: ['github: gh not logged in'] }))
    expect(w.text()).toContain('Nothing to clean up')
    expect(w.text()).toContain('gh not logged in')
    expect(w.find('[data-delete]').exists()).toBe(false)
  })

  it('reloads when the age changes', async () => {
    const w = await open()
    vi.mocked(fetchCleanup).mockResolvedValue(preview({ days: 60, candidates: [] }))
    await w.get('input[data-days]').setValue('60')
    await w.get('input[data-days]').trigger('change')
    await flushPromises()
    expect(fetchCleanup).toHaveBeenLastCalledWith('/r', 60)
  })

  it('shows a load error', async () => {
    vi.mocked(fetchCleanup).mockRejectedValue(new Error('422 cannot determine the default branch'))
    const w = mount(CleanupModal, { props: { repo: '/r', repoName: 'myrepo' } })
    await flushPromises()
    expect(w.text()).toContain('cannot determine the default branch')
  })

  it('closes with Escape and the close button', async () => {
    const w = await open()
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await w.get('[data-close]').trigger('click')
    expect(w.emitted('close')).toHaveLength(2)
  })
})
