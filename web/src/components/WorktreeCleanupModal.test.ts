import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WorktreeCleanupModal from './WorktreeCleanupModal.vue'
import { fetchWorktreeCleanup, runWorktreeCleanup } from '../api'
import type { WtPreview } from '../types'

vi.mock('../api', () => ({ fetchWorktreeCleanup: vi.fn(), runWorktreeCleanup: vi.fn() }))

const preview = (over: Partial<WtPreview> = {}): WtPreview => ({
  days: 7,
  candidates: [
    { path: '/r/done-one', name: 'done-one', branch: 'REG-1-one', sha: 'aaa1111111111111', prNumber: 11, prTitle: 'Fix thing', mergedAt: '2026-08-01T00:00:00Z', kind: 'merged' },
    { path: '/r/done-two', name: 'done-two', branch: 'REG-2-two', sha: 'bbb2222222222222', prNumber: 12, prTitle: 'Add other', mergedAt: '2026-07-01T00:00:00Z', kind: 'merged' },
  ],
  skipped: [{ path: '/r/dirty', name: 'dirty', branch: 'REG-3', prNumber: 13, reason: 'uncommitted changes or untracked files' }],
  warnings: [],
  ...over,
})

const open = async (p: WtPreview = preview()) => {
  vi.mocked(fetchWorktreeCleanup).mockResolvedValue(p)
  const w = mount(WorktreeCleanupModal, { props: { repo: '/r', repoName: 'myrepo' } })
  await flushPromises()
  return w
}

beforeEach(() => {
  vi.mocked(fetchWorktreeCleanup).mockReset()
  vi.mocked(runWorktreeCleanup).mockReset()
})

describe('WorktreeCleanupModal', () => {
  it('lists each worktree folder with its branch and PR, all ticked', async () => {
    const w = await open()
    expect(fetchWorktreeCleanup).toHaveBeenCalledWith('/r', 7, 0)
    expect(w.text()).toContain('done-one')
    expect(w.text()).toContain('REG-1-one')
    expect(w.text()).toContain('#11')
    expect(w.text()).toContain('Fix thing')
    const boxes = w.findAll('input[type=checkbox][data-path]')
    expect(boxes).toHaveLength(2)
    expect(boxes.every((b) => (b.element as HTMLInputElement).checked)).toBe(true)
    expect(w.get('[data-remove]').text()).toContain('Remove 2 worktrees')
  })

  it('warns that folders are deleted, and that branches are kept', async () => {
    const w = await open()
    expect(w.text()).toContain('deletes the folder')
    expect(w.text()).toContain('branch is kept')
  })

  it('says what it kept and why', async () => {
    const w = await open()
    expect(w.text()).toContain('dirty')
    expect(w.text()).toContain('uncommitted changes')
  })

  it('updates the count and disables removal at zero', async () => {
    const w = await open()
    await w.get('input[data-path="/r/done-one"]').setValue(false)
    expect(w.get('[data-remove]').text()).toContain('Remove 1 worktree')
    expect(w.get('[data-remove]').text()).not.toContain('worktrees')
    await w.get('input[data-path="/r/done-two"]').setValue(false)
    expect(w.get('[data-remove]').attributes('disabled')).toBeDefined()
    await w.get('[data-select-all]').trigger('click')
    expect(w.get('[data-remove]').text()).toContain('Remove 2 worktrees')
    await w.get('[data-select-none]').trigger('click')
    expect(w.get('[data-remove]').attributes('disabled')).toBeDefined()
  })

  it('removes only the ticked worktrees, with the commit that was shown', async () => {
    vi.mocked(runWorktreeCleanup).mockResolvedValue([{ path: '/r/done-two', sha: 'bbb2222222222222', removed: true }])
    const w = await open()
    await w.get('input[data-path="/r/done-one"]').setValue(false)
    await w.get('[data-remove]').trigger('click')
    await flushPromises()
    expect(runWorktreeCleanup).toHaveBeenCalledWith('/r', 7, 0, [{ path: '/r/done-two', sha: 'bbb2222222222222' }])
    expect(w.emitted('removed')).toHaveLength(1)
  })

  it('reports each result and points at the branch cleanup', async () => {
    vi.mocked(runWorktreeCleanup).mockResolvedValue([
      { path: '/r/done-one', name: 'done-one', branch: 'REG-1-one', sha: 'a', removed: true },
      { path: '/r/done-two', name: 'done-two', branch: 'REG-2-two', sha: 'b', removed: false, error: 'uncommitted changes appeared since the preview' },
    ])
    const w = await open()
    await w.get('[data-remove]').trigger('click')
    await flushPromises()
    const text = w.text()
    expect(text).toContain('Removed 1 of 2')
    expect(text).toContain('uncommitted changes appeared')
    expect(text).toContain('Clean up branches')
    expect(w.find('[data-remove]').exists()).toBe(false)
  })

  it('shows an error if the request fails and keeps the list', async () => {
    vi.mocked(runWorktreeCleanup).mockRejectedValue(new Error('internal error'))
    const w = await open()
    await w.get('[data-remove]').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('internal error')
    expect(w.findAll('input[type=checkbox][data-path]')).toHaveLength(2)
  })

  it('does not remove twice while a request is in flight', async () => {
    let finish: (v: never[]) => void = () => {}
    vi.mocked(runWorktreeCleanup).mockReturnValue(new Promise((r) => (finish = r as typeof finish)))
    const w = await open()
    await w.get('[data-remove]').trigger('click')
    await w.get('[data-remove]').trigger('click')
    expect(runWorktreeCleanup).toHaveBeenCalledTimes(1)
    finish([])
    await flushPromises()
  })

  it('has an empty state and surfaces warnings', async () => {
    const w = await open(preview({ candidates: [], skipped: [], warnings: ['github: gh not logged in'] }))
    expect(w.text()).toContain('Nothing to remove')
    expect(w.text()).toContain('gh not logged in')
    expect(w.find('[data-remove]').exists()).toBe(false)
  })

  it('reloads when the age changes', async () => {
    const w = await open()
    vi.mocked(fetchWorktreeCleanup).mockResolvedValue(preview({ days: 30, candidates: [] }))
    await w.get('input[data-days]').setValue('30')
    await w.get('input[data-days]').trigger('change')
    await flushPromises()
    expect(fetchWorktreeCleanup).toHaveBeenLastCalledWith('/r', 30, 0)
  })

  it('shows a load error', async () => {
    vi.mocked(fetchWorktreeCleanup).mockRejectedValue(new Error('422 cannot determine the default branch'))
    const w = mount(WorktreeCleanupModal, { props: { repo: '/r', repoName: 'myrepo' } })
    await flushPromises()
    expect(w.text()).toContain('cannot determine the default branch')
  })

  it('closes with Escape and the close button', async () => {
    const w = await open()
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await w.get('[data-close]').trigger('click')
    expect(w.emitted('close')).toHaveLength(2)
  })
  describe('idle worktrees', () => {
    const idle = (over = {}) => ({
      path: '/r/old-one', name: 'old-one', branch: 'LOY-5-old', sha: 'ccc3333333333333', prNumber: 0, prTitle: '', mergedAt: '',
      kind: 'dormant-on-remote' as const, lastActivity: '2026-08-01T00:00:00Z', ...over,
    })

    it('are not asked for until the box is ticked, then the list reloads with the age', async () => {
      const w = await open()
      expect(fetchWorktreeCleanup).toHaveBeenLastCalledWith('/r', 7, 0)
      vi.mocked(fetchWorktreeCleanup).mockResolvedValue(preview({ candidates: [...preview().candidates, idle()] }))
      await w.get('input[data-include-idle]').setValue(true)
      await flushPromises()
      expect(fetchWorktreeCleanup).toHaveBeenLastCalledWith('/r', 7, 14)
    })

    it('start unticked, and say why they are safe', async () => {
      const w = await open()
      vi.mocked(fetchWorktreeCleanup).mockResolvedValue(
        preview({ candidates: [...preview().candidates, idle(), idle({ path: '/r/old-two', name: 'old-two', kind: 'dormant-merged' })] }),
      )
      await w.get('input[data-include-idle]').setValue(true)
      await flushPromises()
      expect((w.get('input[data-path="/r/old-one"]').element as HTMLInputElement).checked).toBe(false)
      expect((w.get('input[data-path="/r/done-one"]').element as HTMLInputElement).checked).toBe(true)
      expect(w.get('[data-kind="dormant-on-remote"]').text()).toContain('pushed to a remote')
      expect(w.get('[data-kind="dormant-merged"]').text()).toContain('already in the default branch')
    })

    it('sends the age when removing', async () => {
      vi.mocked(runWorktreeCleanup).mockResolvedValue([{ path: '/r/old-one', sha: 'ccc3333333333333', removed: true }])
      const w = await open()
      vi.mocked(fetchWorktreeCleanup).mockResolvedValue(preview({ candidates: [idle()] }))
      await w.get('input[data-include-idle]').setValue(true)
      await flushPromises()
      await w.get('input[data-path="/r/old-one"]').setValue(true)
      await w.get('[data-remove]').trigger('click')
      await flushPromises()
      expect(runWorktreeCleanup).toHaveBeenCalledWith('/r', 7, 14, [{ path: '/r/old-one', sha: 'ccc3333333333333' }])
    })
  })
})
