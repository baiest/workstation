// Which projects the user chose to hide. Kept in the browser: it is a view
// preference, and nothing here should write to the user's config files.

const KEY = 'workstation.hidden-repos'

export interface Store {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

/** The browser's localStorage, or undefined when it is blocked (private mode, site data off). */
export function browserStorage(): Store | undefined {
  try {
    return window.localStorage
  } catch {
    return undefined
  }
}

/** Saved paths of hidden projects; anything unreadable or malformed counts as "none hidden". */
export function loadHidden(store: Store | undefined): string[] {
  try {
    const parsed: unknown = JSON.parse(store?.getItem(KEY) ?? '[]')
    if (!Array.isArray(parsed)) return []
    return [...new Set(parsed.filter((p): p is string => typeof p === 'string'))]
  } catch {
    return []
  }
}

export function saveHidden(store: Store | undefined, paths: string[]): void {
  try {
    store?.setItem(KEY, JSON.stringify(paths))
  } catch {
    // ignore: remembering what is hidden is a convenience
  }
}

/** Adds the path if it is visible, removes it if it is hidden. Returns a new list. */
export function toggleHidden(paths: string[], path: string): string[] {
  return paths.includes(path) ? paths.filter((p) => p !== path) : [...paths, path]
}
