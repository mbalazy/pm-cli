// The home screen's group filter lives in the URL (`?g=acme`) so a phone
// bookmark can carry it. This file parses it and assigns the `g <letter>`
// chord letters.

/** The `g` search param as a string, '' when absent or malformed. */
export function parseGroupFilter(search: Record<string, unknown>): string {
  const g = search.g
  return typeof g === 'string' ? g : ''
}

/** The search object for a filter value: `{}` clears it (no `?g=` in the URL). */
export function groupSearch(slug: string): { g?: string } {
  return slug === '' ? {} : { g: slug }
}

/**
 * One hotkey letter per group for the `g <letter>` chord: the first letter
 * of the slug that no earlier group took, then any free letter of the slug,
 * then none. Deterministic in the given order, so the sidebar and the chord
 * agree.
 */
export function groupHotkeys(groups: { slug: string }[]): Map<string, string> {
  const byLetter = new Map<string, string>()
  const taken = new Set<string>()
  for (const g of groups) {
    const letters = g.slug.toLowerCase().replace(/[^a-z]/g, '')
    let pick = ''
    for (const ch of letters) {
      if (!taken.has(ch)) {
        pick = ch
        break
      }
    }
    if (pick === '') continue
    taken.add(pick)
    byLetter.set(pick, g.slug)
  }
  return byLetter
}

/** True for a key that can complete the `g <letter>` chord. */
export function isGroupLetter(key: string): boolean {
  return /^[a-z]$/.test(key)
}
