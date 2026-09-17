import type { SaveRequest } from '@configbutler/krm-stream'

import type { Database, DatabaseSpec } from './databaseTypes'
import { requestJson } from './http'
import type { SaveReceipt } from './liveEditableResource'

/** Every Database in the demo namespace, projected exactly as the live stream
 *  projects them so the first paint and the watch agree about shape. */
export async function listDatabases(): Promise<{ items: Database[] }> {
  return await requestJson<{ items: Database[] }>('/public/databases', {
    cache: 'no-store',
  })
}

export async function getDatabase(name: string): Promise<Database> {
  return await requestJson<Database>(
    `/public/databases/${encodeURIComponent(name)}`,
    { cache: 'no-store' },
  )
}

/** The note travels as a header rather than in the body, because it describes
 *  the SAVE and not the object: the same header the coffee editor sends, read
 *  by the same helper on the backend. */
function withReason(reason?: string): Headers {
  const headers = new Headers({ 'content-type': 'application/json' })
  if (reason?.trim()) {
    headers.set('x-change-reason', reason.trim())
  }
  return headers
}

export type CreateDatabaseResult = SaveReceipt & { name: string }

export async function createDatabase(
  name: string,
  spec: DatabaseSpec,
  reason?: string,
): Promise<CreateDatabaseResult> {
  return await requestJson<CreateDatabaseResult>('/public/databases', {
    method: 'POST',
    headers: withReason(reason),
    body: JSON.stringify({ name, spec }),
  })
}

export async function patchDatabase(
  name: string,
  intent: SaveRequest,
  options?: { reason?: string },
): Promise<SaveReceipt> {
  return await requestJson<SaveReceipt>(
    `/public/databases/${encodeURIComponent(name)}`,
    {
      method: 'PATCH',
      headers: withReason(options?.reason),
      body: JSON.stringify(intent),
    },
  )
}
