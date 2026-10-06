// Kubernetes, through krm-foyer's /k8s.
//
// Every request here is the signed-in person's: krm-foyer forwards it with
// their own token, so the API server's answer -- a 403, a 409, a 422 about a
// field -- is the answer, and the audit log names them. This module only
// addresses objects and shapes requests; it decides nothing.

import {
  nativeCollectionURL,
  nativeObjectURL,
  type SaveRequest,
} from '@configbutler/krm-stream'

import { appConfig } from './appConfig'
import { requestJson } from './http'

/** A resource, as the API server names it. An empty group is the core API. */
export interface ResourceRef {
  group: string
  version: string
  resource: string
}

export const COFFEECONFIGS: ResourceRef = {
  group: 'examples.configbutler.ai',
  version: 'v1alpha1',
  resource: 'coffeeconfigs',
}
export const QUIZSESSIONS: ResourceRef = {
  group: 'examples.configbutler.ai',
  version: 'v1alpha1',
  resource: 'quizsessions',
}
export const QUIZSUBMISSIONS: ResourceRef = {
  group: 'examples.configbutler.ai',
  version: 'v1alpha1',
  resource: 'quizsubmissions',
}
export const DATABASES: ResourceRef = {
  group: 'platform.configbutler.ai',
  version: 'v1alpha1',
  resource: 'databases',
}
/** gitops-reverser serves CommitRequest at v1alpha3 only. */
export const COMMITREQUESTS: ResourceRef = {
  group: 'configbutler.ai',
  version: 'v1alpha3',
  resource: 'commitrequests',
}
export const ROOMS: ResourceRef = {
  group: 'room-pass.koudijs.dev',
  version: 'v1alpha1',
  resource: 'rooms',
}
export const PARTICIPANTS: ResourceRef = {
  group: 'room-pass.koudijs.dev',
  version: 'v1alpha1',
  resource: 'participants',
}
export const ROLES: ResourceRef = {
  group: 'rbac.authorization.k8s.io',
  version: 'v1',
  resource: 'roles',
}
export const ROLEBINDINGS: ResourceRef = {
  group: 'rbac.authorization.k8s.io',
  version: 'v1',
  resource: 'rolebindings',
}

const K8S = '/k8s'

/** Who the API server records in managedFields for a write from these pages.
 *  Left out, it takes the browser's User-Agent, and every field reads as owned
 *  by "Mozilla". */
const WRITER = '?fieldManager=voter'

/** The application's namespace unless told otherwise. */
const namespaceOr = (namespace?: string) => namespace ?? appConfig().namespace

export function objectURL(
  ref: ResourceRef,
  name: string,
  namespace?: string,
): string {
  return nativeObjectURL(K8S, { ...ref, namespace: namespaceOr(namespace), name })
}

export function collectionURL(ref: ResourceRef, namespace?: string): string {
  return nativeCollectionURL(K8S, { ...ref, namespace: namespaceOr(namespace) })
}

type Obj = {
  apiVersion?: string
  kind?: string
  metadata?: { annotations?: Record<string, string> } & Record<string, unknown>
}

const LAST_APPLIED = 'kubectl.kubernetes.io/last-applied-configuration'

/** The stream's krm-full/v1 projection, for a read that does not come over the
 *  stream: no managedFields and no last-applied annotation. The editor
 *  reconciles a read against what the stream delivered, and the two must agree
 *  about an object's shape (krm-stream's LiveResourceStore docs). Neither kind
 *  edited here is a Secret, which is the projection's only other rule. */
export function project<T>(object: T): T {
  const copy = structuredClone(object) as Obj
  const metadata = copy.metadata
  if (metadata) {
    delete metadata.managedFields
    if (metadata.annotations) {
      delete metadata.annotations[LAST_APPLIED]
      // Empty only because we emptied it: leaving {} would claim the object
      // has an annotation map, which is a different fact from having none.
      if (Object.keys(metadata.annotations).length === 0) {
        delete metadata.annotations
      }
    }
  }
  return copy as T
}

export async function getObject<T>(
  ref: ResourceRef,
  name: string,
  namespace?: string,
): Promise<T> {
  return project(
    await requestJson<T>(objectURL(ref, name, namespace), { cache: 'no-store' }),
  )
}

/** Every object of a kind in the namespace, projected. A list's items carry no
 *  apiVersion or kind of their own for every type, so they are filled in from
 *  the list's, which is what a stream event would carry. */
export async function listObjects<T>(
  ref: ResourceRef,
  namespace?: string,
): Promise<T[]> {
  const list = await requestJson<{
    apiVersion: string
    kind: string
    items: Obj[]
  }>(collectionURL(ref, namespace), { cache: 'no-store' })
  const kind = list.kind.replace(/List$/, '')
  return list.items.map((item) =>
    project({ apiVersion: list.apiVersion, kind, ...item } as T),
  )
}

export async function createObject<T>(
  ref: ResourceRef,
  object: unknown,
  namespace?: string,
): Promise<T> {
  return await requestJson<T>(collectionURL(ref, namespace) + WRITER, {
    method: 'POST',
    body: JSON.stringify(object),
  })
}

export async function mergePatch<T>(
  ref: ResourceRef,
  name: string,
  patch: unknown,
  namespace?: string,
): Promise<T> {
  return await requestJson<T>(objectURL(ref, name, namespace) + WRITER, {
    method: 'PATCH',
    headers: { 'content-type': 'application/merge-patch+json' },
    body: JSON.stringify(patch),
  })
}

export async function deleteObject(
  ref: ResourceRef,
  name: string,
  namespace?: string,
): Promise<void> {
  await requestJson<unknown>(objectURL(ref, name, namespace), {
    method: 'DELETE',
  })
}

/** The merge patch for an editor's save: its spec changes, made conditional on
 *  the object it was editing. `metadata.uid` and `metadata.resourceVersion` in
 *  a merge patch are preconditions -- the API server answers 409 if the object
 *  was replaced or has moved on -- so two editors can never overwrite each
 *  other unseen.
 *
 *  Only `spec` is sent. The editability policy says the same, and admission
 *  is what holds it for anyone who writes to /k8s by hand
 *  (voter/config/admission/); this keeps an editor from even asking.
 *  `annotations` go beside the preconditions, for the one annotation a save
 *  may carry: its reason, on a Database. */
export function conditionalPatch(
  intent: SaveRequest,
  annotations?: Record<string, string>,
): Record<string, unknown> {
  for (const key of Object.keys(intent.patch)) {
    if (key !== 'spec') {
      throw new Error('Only spec is editable.')
    }
  }
  return {
    ...intent.patch,
    metadata: {
      uid: intent.uid,
      resourceVersion: intent.resourceVersion,
      ...(annotations ? { annotations } : {}),
    },
  }
}

/** What a save reports about its CommitRequest, when it asked for one. */
export interface CommitReceipt {
  commitRequested?: boolean
  commitRequest?: string
  commitError?: string
}

/** Asks ConfigButler to commit the change just made, as this person: a
 *  CommitRequest naming the GitTarget, with the editor's reason as the commit
 *  message. An empty target means the deployment has none, and nothing is asked.
 *
 *  The save and this are two Kubernetes writes. If this one fails the save
 *  still happened, so it reports PARTIAL success rather than an error. */
export async function requestCommit(
  target: string,
  generateName: string,
  reason: string,
): Promise<CommitReceipt> {
  if (target === '') return {}
  const config = appConfig()
  const namespace = config.commitRequestNamespace || config.namespace
  const spec: Record<string, unknown> = { gitTargetRef: { name: target } }
  if (reason !== '') spec.message = reason
  // Without this the request races the write it exists to publish, and loses:
  // nothing is attached, so the message is dropped and the edit waits out the
  // target's own window instead.
  if (config.commitCloseDelaySeconds > 0) {
    spec.closeDelaySeconds = config.commitCloseDelaySeconds
  }
  try {
    const created = await createObject<{ metadata: { name: string } }>(
      COMMITREQUESTS,
      {
        apiVersion: 'configbutler.ai/v1alpha3',
        kind: 'CommitRequest',
        metadata: { generateName, namespace },
        spec,
      },
      namespace,
    )
    return { commitRequested: true, commitRequest: created.metadata.name }
  } catch {
    return {
      commitRequested: false,
      commitError:
        'Your change was saved to Kubernetes, but asking ConfigButler to commit it failed.',
    }
  }
}

/** The reason an editor typed, trimmed and bounded, so neither a Git commit
 *  message nor an annotation is handed an unbounded string. */
export function changeReason(reason?: string): string {
  return (reason ?? '').trim().slice(0, 1024).trim()
}
