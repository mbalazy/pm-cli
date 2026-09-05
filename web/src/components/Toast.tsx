import { cn } from '@/components/ui/cn'

// A one-line notice after a mutation: "saved" or the server's error text.
// Presentation only; the message and its lifetime are the hook's.

export function Toast({ message, error }: { message: string; error?: boolean }) {
  if (message === '') return null
  return (
    <div
      role="status"
      className={cn(
        'toast-in fixed right-4 bottom-[max(1rem,env(safe-area-inset-bottom))] z-30 max-w-[min(32rem,90vw)] rounded-sm border px-3 py-1.5 text-sm shadow-lg',
        error ? 'border-crit bg-crit-bg text-crit' : 'border-rule-strong bg-paper text-ink',
      )}
    >
      {message}
    </div>
  )
}
