// A one-line notice after a mutation: "saved" or the server's error text.
// Presentation only; the message and its lifetime are the hook's.

export function Toast({ message, error }: { message: string; error?: boolean }) {
  if (message === '') return null
  return (
    <div
      role="status"
      className={`fixed right-4 bottom-4 rounded border px-3 py-1 text-sm shadow ${error ? 'border-red-300 bg-red-50 text-red-800' : 'bg-white'}`}
    >
      {message}
    </div>
  )
}
