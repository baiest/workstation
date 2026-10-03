import { describe, expect, it, vi } from 'vitest'
import { loadBranchData, newBranchState, type BranchFetcher } from './branchLoader'
import type { BranchesResponse } from './types'

const resp = (nodes: string[], prsPending = false): BranchesResponse => ({
  graph: { default: 'main', hidden: 0, nodes: nodes.map((b) => ({ branch: b, isDefault: b === 'main', merged: false, ahead: 0, behind: 0, tip: 't', date: 'd' })) },
  warnings: [],
  prsPending,
})

// a promise we resolve by hand, to look at the state in between the two steps
const deferred = <T>() => {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => ((resolve = res), (reject = rej)))
  return { promise, resolve, reject }
}

describe('loadBranchData', () => {
  it('shows the graph from git first, then replaces it once the pull requests arrive', async () => {
    const full = deferred<BranchesResponse>()
    const fetcher: BranchFetcher = vi
      .fn()
      .mockResolvedValueOnce(resp(['main', 'a'], true)) // git only
      .mockReturnValueOnce(full.promise) // with PRs
    const st = newBranchState()

    const done = loadBranchData(st, '/r', false, fetcher)
    await vi.waitFor(() => expect(st.data).toBeDefined())

    // the graph is on screen while PRs are still loading
    expect(st.data!.graph.nodes.map((n) => n.branch)).toEqual(['main', 'a'])
    expect(st.prsLoading).toBe(true)
    expect(fetcher).toHaveBeenNthCalledWith(1, '/r', { merged: false, prs: false })

    full.resolve(resp(['main', 'a', 'b']))
    await done
    expect(st.data!.graph.nodes).toHaveLength(3)
    expect(st.prsLoading).toBe(false)
    expect(st.loading).toBe(false)
    expect(fetcher).toHaveBeenNthCalledWith(2, '/r', { merged: false, refresh: false })
  })

  it('does not blank the graph when reloading: it skips the git-only step', async () => {
    const fetcher: BranchFetcher = vi.fn().mockResolvedValue(resp(['main']))
    const st = newBranchState()
    st.data = resp(['main', 'old'])

    await loadBranchData(st, '/r', true, fetcher)
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(fetcher).toHaveBeenCalledWith('/r', { merged: false, refresh: true })
    expect(st.data!.graph.nodes).toHaveLength(1)
  })

  it('keeps the graph and reports the problem if only the pull requests fail', async () => {
    const fetcher: BranchFetcher = vi
      .fn()
      .mockResolvedValueOnce(resp(['main', 'a'], true))
      .mockRejectedValueOnce(new Error('gh: not logged in'))
    const st = newBranchState()

    await loadBranchData(st, '/r', false, fetcher)
    expect(st.data!.graph.nodes).toHaveLength(2)
    expect(st.error).toBe('')
    expect(st.prsError).toContain('not logged in')
    expect(st.prsLoading).toBe(false)
    expect(st.loading).toBe(false)
  })

  it('reports an error and skips the second step if the git graph fails', async () => {
    const fetcher: BranchFetcher = vi.fn().mockRejectedValue(new Error('422 cannot determine the default branch'))
    const st = newBranchState()

    await loadBranchData(st, '/r', false, fetcher)
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(st.data).toBeUndefined()
    expect(st.error).toContain('default branch')
    expect(st.loading).toBe(false)
  })

  it('clears an old PR error on the next successful load', async () => {
    const st = newBranchState()
    st.data = resp(['main'])
    st.prsError = 'old problem'
    await loadBranchData(st, '/r', false, vi.fn().mockResolvedValue(resp(['main'])))
    expect(st.prsError).toBe('')
  })

  it('ignores a second call while one is running', async () => {
    const first = deferred<BranchesResponse>()
    const fetcher: BranchFetcher = vi.fn().mockReturnValue(first.promise)
    const st = newBranchState()

    const a = loadBranchData(st, '/r', false, fetcher)
    await loadBranchData(st, '/r', false, fetcher)
    expect(fetcher).toHaveBeenCalledTimes(1)
    first.resolve(resp(['main']))
    await a
  })

  describe('old cached pull requests', () => {
    const stale = (nodes: string[]): BranchesResponse => ({ ...resp(nodes), prsStale: true, prsFetchedAt: '2026-10-02T10:00:00Z' })
    const noWait = () => Promise.resolve()

    it('shows the cached data at once and asks again a moment later for the refreshed one', async () => {
      const sleeps: number[] = []
      const fetcher: BranchFetcher = vi
        .fn()
        .mockResolvedValueOnce(resp(['main', 'a'], true)) // git only
        .mockResolvedValueOnce(stale(['main', 'a'])) // the server answered from cache, refreshing behind
        .mockResolvedValueOnce(resp(['main', 'a', 'b'])) // the refreshed data
      const st = newBranchState()

      await loadBranchData(st, '/r', false, fetcher, (ms) => (sleeps.push(ms), Promise.resolve()))

      expect(fetcher).toHaveBeenCalledTimes(3)
      expect(fetcher).toHaveBeenNthCalledWith(3, '/r', { merged: false, refresh: false }) // a poll never forces a fetch
      expect(sleeps).toHaveLength(1)
      expect(sleeps[0]).toBeGreaterThanOrEqual(1000)
      expect(st.data!.graph.nodes).toHaveLength(3)
      expect(st.data!.prsStale).toBeFalsy()
      expect(st.prsLoading).toBe(false)
      expect(st.loading).toBe(false)
    })

    it('keeps showing the loading state while it waits for the refresh', async () => {
      const seen: boolean[] = []
      const st = newBranchState()
      const fetcher: BranchFetcher = vi
        .fn()
        .mockResolvedValueOnce(resp(['main'], true))
        .mockResolvedValueOnce(stale(['main']))
        .mockResolvedValueOnce(resp(['main']))
      await loadBranchData(st, '/r', false, fetcher, () => (seen.push(st.prsLoading), Promise.resolve()))
      expect(seen).toEqual([true])
    })

    it('gives up after a few polls instead of waiting forever', async () => {
      const fetcher: BranchFetcher = vi.fn().mockResolvedValue(stale(['main']))
      const st = newBranchState()
      st.data = stale(['main']) // skip the git-only step
      await loadBranchData(st, '/r', false, fetcher, noWait)
      expect(vi.mocked(fetcher).mock.calls.length).toBeLessThanOrEqual(5)
      expect(st.loading).toBe(false)
      expect(st.prsLoading).toBe(false)
    })

    it('does not poll when the data is fresh', async () => {
      const sleeps = vi.fn().mockResolvedValue(undefined)
      const fetcher: BranchFetcher = vi.fn().mockResolvedValue(resp(['main']))
      await loadBranchData(newBranchState(), '/r', false, fetcher, sleeps)
      expect(sleeps).not.toHaveBeenCalled()
    })

    it('ignores a failing poll and keeps what it has', async () => {
      const fetcher: BranchFetcher = vi
        .fn()
        .mockResolvedValueOnce(resp(['main'], true))
        .mockResolvedValueOnce(stale(['main', 'a']))
        .mockRejectedValueOnce(new Error('network down'))
      const st = newBranchState()
      await loadBranchData(st, '/r', false, fetcher, noWait)
      expect(st.data!.graph.nodes).toHaveLength(2)
      expect(st.prsError).toBe('')
      expect(st.prsLoading).toBe(false)
    })
  })

  it('passes the "recently merged" choice to both steps', async () => {
    const fetcher: BranchFetcher = vi.fn().mockResolvedValue(resp(['main']))
    const st = newBranchState()
    st.merged = true
    await loadBranchData(st, '/r', false, fetcher)
    expect(fetcher).toHaveBeenNthCalledWith(1, '/r', { merged: true, prs: false })
    expect(fetcher).toHaveBeenNthCalledWith(2, '/r', { merged: true, refresh: false })
  })
})
