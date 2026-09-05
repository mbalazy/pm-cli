// The cockpit's screens and their keys - one table for the sidebar, the phone
// bar and the help dialog's wording.
export const SCREENS = [
  { to: '/', label: 'Today', key: '1' },
  { to: '/changes', label: 'Changes', key: '2' },
  { to: '/runs', label: 'Runs', key: '3' },
  { to: '/settings', label: 'Settings', key: ',' },
] as const
