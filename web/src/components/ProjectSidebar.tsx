import { Link } from '@tanstack/react-router'

// Presentation only: the list of projects with their open-task badge. Which
// one is active and what the counts are is decided upstream.

export interface ProjectSidebarItem {
  slug: string
  name: string
  openTasks: number
}

interface Props {
  items: ProjectSidebarItem[]
  activeSlug?: string
}

export function ProjectSidebar({ items, activeSlug }: Props) {
  return (
    <nav aria-label="Projects" className="p-3">
      <h2 className="mb-2 text-sm uppercase text-gray-500">Projects</h2>
      <ul className="space-y-1">
        {items.map((p) => (
          <li key={p.slug}>
            <Link
              to="/p/$slug"
              params={{ slug: p.slug }}
              className={`block px-2 py-1 hover:underline ${p.slug === activeSlug ? 'font-bold' : ''}`}
              aria-current={p.slug === activeSlug ? 'page' : undefined}
            >
              {p.name} <span className="text-gray-500">({p.openTasks})</span>
            </Link>
          </li>
        ))}
      </ul>
    </nav>
  )
}
