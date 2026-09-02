import '@testing-library/jest-dom/vitest'

// jsdom has no layout; the router scrolls on navigation and jsdom logs
// "Not implemented" for it. Silence it, nothing else.
window.scrollTo = () => {}
