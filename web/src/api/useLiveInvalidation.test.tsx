import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { useLiveInvalidation } from './useLiveInvalidation'

// A hand-rolled EventSource: records listeners so the test can fire events.
class FakeEventSource {
  static last: FakeEventSource | undefined
  listeners = new Map<string, (e: MessageEvent<string>) => void>()
  closed = false
  url: string
  constructor(url: string) {
    this.url = url
    FakeEventSource.last = this
  }
  addEventListener(type: string, fn: (e: MessageEvent<string>) => void) {
    this.listeners.set(type, fn)
  }
  close() {
    this.closed = true
  }
  fire(type: string, data: string) {
    this.listeners.get(type)?.({ data } as MessageEvent<string>)
  }
}

function Probe() {
  useLiveInvalidation()
  return null
}

afterEach(() => vi.unstubAllGlobals())

describe('useLiveInvalidation', () => {
  it('invalidates the project keys on tasks and the runs key on runs, closes on unmount', () => {
    vi.stubGlobal('EventSource', FakeEventSource)
    const client = new QueryClient()
    const spy = vi.spyOn(client, 'invalidateQueries')
    const view = render(
      <QueryClientProvider client={client}>
        <Probe />
      </QueryClientProvider>,
    )
    const es = FakeEventSource.last!
    expect(es.url).toBe('/api/events')

    es.fire('tasks', '{"project":"alpha"}')
    expect(spy.mock.calls.map((c) => c[0]?.queryKey)).toEqual([
      ['tasks', 'alpha'],
      ['task', 'alpha'],
      ['context', 'alpha'],
      ['projects'],
    ])

    spy.mockClear()
    es.fire('runs', '{"project":"alpha"}')
    expect(spy.mock.calls.map((c) => c[0]?.queryKey)).toEqual([['runs']])

    spy.mockClear()
    es.fire('tasks', 'not json')
    expect(spy).not.toHaveBeenCalled()

    view.unmount()
    expect(es.closed).toBe(true)
  })

  it('is a no-op where EventSource does not exist', () => {
    vi.stubGlobal('EventSource', undefined)
    const client = new QueryClient()
    expect(() =>
      render(
        <QueryClientProvider client={client}>
          <Probe />
        </QueryClientProvider>,
      ),
    ).not.toThrow()
  })
})
