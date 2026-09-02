# pm web cockpit

The React front end `pm serve` embeds. Deliberately raw until the target views are designed - it has to work, not to look good.

**Layer rule.** `src/api/` (types mirroring the Go DTOs, the fetch client, react-query hooks, the live feed) and `src/lib/` (pure functions, each with a test) are permanent. `src/routes/` (composition), `src/components/` (presentation: props in, JSX out - no fetching, no domain logic) and `src/hooks/` (React glue around `lib/` rules) are the replaceable view layer. A visual pass replaces those directories and nothing else.

**What it does.** Project sidebar, tasks by status, task detail (markdown, Spec/Log zones), Runs table with an explicit remote fetch, ⌘K palette, `j`/`k`/`Enter`/`Esc`/`?` shortcuts, live refetch from `/api/events`, a phone layout under 768 px and a PWA manifest. Read-only: nothing here mutates pm data.

```sh
npm ci            # or: make web-install (repo root)
npm run dev       # HMR dev server; /api is proxied to pm serve on 127.0.0.1:7070
npm run lint      # oxlint + prettier --check
npm run typecheck # tsc -b
npm test -- --run # vitest
npm run build     # bundle into ../internal/server/dist (make web also restores .gitkeep)
```

From the repo root: `make web-check`, `make web`, `make install-full`.
