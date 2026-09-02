// The one fetch wrapper. Every endpoint is a GET returning JSON; every error is
// `{"error": "..."}` with 400 (caller's mistake), 404 (not there) or 500.

export class ApiError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

/** GET `path` (already prefixed with /api) and decode the JSON body as T. */
export async function apiGet<T>(path: string): Promise<T> {
  const res = await fetch(path, { headers: { Accept: 'application/json' } })
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`
    try {
      const body = (await res.json()) as { error?: string }
      if (body.error) message = body.error
    } catch {
      // Not JSON (a proxy page, an empty body): the status line is the message.
    }
    throw new ApiError(res.status, message)
  }
  return (await res.json()) as T
}

/** Builds a query string from the defined, non-empty values only. */
export function query(params: Record<string, string | number | undefined>): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '') q.set(k, String(v))
  }
  const s = q.toString()
  return s ? `?${s}` : ''
}
