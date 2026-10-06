import type { SaveRequest } from '@configbutler/krm-stream'

import { appConfig } from './appConfig'
import type { Database, DatabaseSpec } from './databaseTypes'
import {
  DATABASES,
  changeReason,
  conditionalPatch,
  createObject,
  getObject,
  listObjects,
  mergePatch,
  requestCommit,
} from './kube'
import type { SaveReceipt } from './liveEditableResource'

/** The note the requester typed into "why are you making this change?".
 *
 *  For the coffee menu that note becomes a Git commit message and nothing else,
 *  because the CoffeeConfig itself is in Git and the commit is the record. A
 *  Database has no GitTarget watching it yet, so the note would simply be
 *  dropped -- and the note is half of what the list page exists to show. It
 *  belongs on the object either way: when a target does start watching these,
 *  the annotation travels into Git with the spec it explains. */
export const INTENT_ANNOTATION = 'platform.configbutler.ai/intent'

/** A Kubernetes object name, checked here so a bad one is a readable sentence
 *  rather than the API server's 422 about a regular expression. */
const NAME = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/

/** Every Database in the demo namespace, projected exactly as the live stream
 *  projects them so the first paint and the watch agree about shape. */
export async function listDatabases(): Promise<{ items: Database[] }> {
  return { items: await listObjects<Database>(DATABASES) }
}

export async function getDatabase(name: string): Promise<Database> {
  return await getObject<Database>(DATABASES, name)
}

export type CreateDatabaseResult = SaveReceipt & { name: string }

/** Files a request as this person. The CRD's own required fields, enums and
 *  patterns are the validation: restating them here would give the room two
 *  answers to one question, and the API server's is the one that counts, so
 *  its message is what the screen shows. */
export async function createDatabase(
  name: string,
  spec: DatabaseSpec,
  reason?: string,
): Promise<CreateDatabaseResult> {
  const trimmed = name.trim()
  if (!NAME.test(trimmed)) {
    throw new Error(
      'The name must be lowercase letters, digits and dashes, and start and end with a letter or digit.',
    )
  }
  const note = changeReason(reason)
  const created = await createObject<Database>(DATABASES, {
    apiVersion: 'platform.configbutler.ai/v1alpha1',
    kind: 'Database',
    metadata: {
      name: trimmed,
      namespace: appConfig().namespace,
      ...(note ? { annotations: { [INTENT_ANNOTATION]: note } } : {}),
    },
    spec,
  })
  return {
    saved: true,
    name: created.metadata?.name ?? trimmed,
    ...(await requestCommit(
      appConfig().databaseGitTargetName,
      'database-create-',
      note,
    )),
  }
}

/** The note goes in the same merge patch as the spec it explains, so one write
 *  carries both and there is no moment at which the object records a change
 *  nobody gave a reason for. */
export async function patchDatabase(
  name: string,
  intent: SaveRequest,
  options?: { reason?: string },
): Promise<SaveReceipt> {
  const note = changeReason(options?.reason)
  await mergePatch(
    DATABASES,
    name,
    conditionalPatch(intent, note ? { [INTENT_ANNOTATION]: note } : undefined),
  )
  return {
    saved: true,
    ...(await requestCommit(
      appConfig().databaseGitTargetName,
      'database-save-',
      note,
    )),
  }
}
