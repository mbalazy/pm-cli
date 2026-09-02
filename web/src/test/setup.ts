import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'

// No vitest globals, so testing-library's automatic cleanup never registers.
afterEach(cleanup)

// jsdom has no layout; the router scrolls on navigation and jsdom logs
// "Not implemented" for it. Silence it, nothing else.
window.scrollTo = () => {}
Element.prototype.scrollIntoView ??= () => {}

// jsdom's <dialog> has no showModal/close: the open attribute is the whole
// behaviour the components rely on, so that is what the shim does.
if (typeof HTMLDialogElement !== 'undefined') {
  const proto = HTMLDialogElement.prototype
  proto.showModal ??= function (this: HTMLDialogElement) {
    this.setAttribute('open', '')
  }
  proto.close ??= function (this: HTMLDialogElement) {
    this.removeAttribute('open')
    this.dispatchEvent(new Event('close'))
  }
}

// cmdk observes its list's size; jsdom has no ResizeObserver.
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
}
