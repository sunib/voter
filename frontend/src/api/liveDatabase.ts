import type { KRMObject } from '@configbutler/krm-stream'

import { getDatabase, patchDatabase } from './databases'
import type { Database } from './databaseTypes'
import { useLiveEditableResource } from './liveEditableResource'

function databaseResource(object: unknown): object is KRMObject & Database {
  if (!object || typeof object !== 'object') return false
  const resource = object as KRMObject
  return (
    resource.apiVersion === 'platform.configbutler.ai/v1alpha1' &&
    resource.kind === 'Database' &&
    typeof resource.metadata?.uid === 'string' &&
    typeof resource.metadata.resourceVersion === 'string' &&
    !!resource.spec &&
    typeof resource.spec === 'object' &&
    !Array.isArray(resource.spec)
  )
}

/** One database request, live and editable. The engine is the same one the
 *  coffee menu editor uses — see liveEditableResource.ts. */
export function useLiveDatabase(namespace: string, name: string) {
  return useLiveEditableResource<Database>({
    scope: {
      group: 'platform.configbutler.ai',
      version: 'v1alpha1',
      resource: 'databases',
      namespace,
      name,
    },
    editable: true,
    read: () => getDatabase(name),
    write: (intent, reason) => patchDatabase(name, intent, { reason }),
    isExpectedKind: databaseResource,
    copy: {
      invalid: 'The stream returned something that is not a Database.',
      replaced:
        'This database request was removed or replaced. Open it again from the list.',
      refreshed:
        'Request refreshed. Your edits are intact; review and save again.',
    },
  })
}
