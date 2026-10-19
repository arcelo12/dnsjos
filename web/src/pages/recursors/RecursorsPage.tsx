import { RequireAdmin } from '@/app/auth'
import { PageHeader } from '@/components/PageHeader'
import { AddRecursorDialog } from '@/components/recursors/AddRecursorDialog'
import { PendingRecursorTokens, RecursorsTable } from '@/components/recursors/RecursorsTable'
import { useRecursors } from '@/lib/api/client'

export default function RecursorsPage() {
  const recursors = useRecursors()
  return (
    <>
      <PageHeader
        title="Recursors"
        description="Recursive resolvers your nodes use as upstreams: installed, configured and monitored from here."
        actions={
          <RequireAdmin>
            <AddRecursorDialog />
          </RequireAdmin>
        }
      />
      <RecursorsTable rows={recursors.data?.items} loading={recursors.isPending} error={recursors.error} />
      <RequireAdmin>
        <PendingRecursorTokens />
      </RequireAdmin>
    </>
  )
}
