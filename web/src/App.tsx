import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider, type RouterHistory } from '@tanstack/react-router'
import { useState } from 'react'

import { createAppRouter } from './routes/router'

interface Props {
  history?: RouterHistory
  queryClient?: QueryClient
}

export function App({ history, queryClient }: Props) {
  const [router] = useState(() => createAppRouter(history))
  const [client] = useState(() => queryClient ?? new QueryClient())
  return (
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}
