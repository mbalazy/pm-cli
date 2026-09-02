# pm web cockpit

The React front end `pm serve` embeds. Deliberately raw until the target views are designed - it has to work, not to look good.

**Layer rule.** `src/api/` (types mirroring the Go DTOs, the fetch client, react-query hooks) and `src/lib/` (pure functions, each with a test) are permanent. `src/routes/` (composition) and `src/components/` (presentation: props in, JSX out - no fetching, no domain logic) are the replaceable view layer. A visual pass replaces those two directories and nothing else.

```sh
npm ci            # or: make web-install (repo root)
npm run dev       # HMR dev server; /api is proxied to pm serve on 127.0.0.1:7070
npm run lint      # oxlint + prettier --check
npm run typecheck # tsc -b
npm test -- --run # vitest
npm run build     # bundle into ../internal/server/dist (make web also restores .gitkeep)
```

From the repo root: `make web-check`, `make web`, `make install-full`.
