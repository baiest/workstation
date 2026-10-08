// View preferences kept in the browser. localStorage can be blocked (private
// mode, site data off), so every read and write is allowed to fail: a preference
// is a convenience, never something the page depends on.
import { ref, type Ref } from 'vue'

export function readPref<T>(key: string, fallback: T, parse: (raw: string) => T | undefined): T {
  try {
    const raw = localStorage.getItem(key)
    if (raw === null) return fallback
    return parse(raw) ?? fallback
  } catch {
    return fallback
  }
}

export function writePref(key: string, value: string): void {
  try {
    localStorage.setItem(key, value)
  } catch {
    // ignore: remembering the choice is a convenience
  }
}

/** A ref that starts from the saved value and saves every change made through `set`. */
export function pref<T>(
  key: string,
  fallback: T,
  parse: (raw: string) => T | undefined,
  serialize: (v: T) => string = String,
): { value: Ref<T>; set: (v: T) => void } {
  const value = ref(readPref(key, fallback, parse)) as Ref<T>
  return {
    value,
    set(v: T) {
      value.value = v
      writePref(key, serialize(v))
    },
  }
}

/** Parser for a fixed set of strings: anything else is "unknown" and falls back to the default. */
export const oneOf =
  <T extends string>(...allowed: T[]) =>
  (raw: string): T | undefined =>
    allowed.find((a) => a === raw)
