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

    expect(runCleanup).toHaveBeenCalledWith('/r', 30, [{ branch: 'old-two', sha: 'bbb2222222222222' }])
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
