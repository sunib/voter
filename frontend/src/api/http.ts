// The one fetch wrapper.
//
// Every call in this SPA goes to this application's own backend, which then
// talks to Kubernetes with the signed-in person's token. So every call needs the
// same three things: the session cookie, the CSRF header on anything that
// changes something, and an error that carries the status code — because a 403
// from Kubernetes is the demo, and a screen has to be able to tell it apart
// from a 409 or a network fault.
//
// It lives here rather than in coffee.ts because it is not about coffee. It was
// in coffee.ts, and the second caller had to import its own transport from a
// module about vouchers and SKUs.

import { currentCsrfToken } from './session'

export type ApiError = Error & {
  status: number
  body?: unknown
}

export function createApiError(status: number, body: unknown): ApiError {
  const details = body as { message?: unknown; error?: unknown } | null
  const message =
    typeof body === 'string'
      ? body
      : typeof details?.message === 'string'
        ? details.message
        : typeof details?.error === 'string'
          ? details.error
          : `Request failed (${status})`
  const err = new Error(message) as ApiError
  err.status = status
  err.body = body
  return err
}

async function readJsonOrText(res: Response): Promise<unknown> {
  const contentType = res.headers.get('content-type') ?? ''
  if (contentType.includes('application/json')) {
    return await res.json()
  }
  return await res.text()
}

export async function requestJson<T>(
  path: string,
  init?: RequestInit,
): Promise<T> {
  const headers = new Headers(init?.headers)
  if (init?.body !== undefined && !headers.has('content-type')) {
    headers.set('content-type', 'application/json')
  }

  // Every cookie-authenticated mutation needs CSRF proof, or the backend
  // answers 403. GET and HEAD are not mutations and the backend does not ask.
  const method = (init?.method ?? 'GET').toUpperCase()
  if (method !== 'GET' && method !== 'HEAD' && !headers.has('x-csrf-token')) {
    headers.set('x-csrf-token', currentCsrfToken())
  }

  const res = await fetch(path, {
    ...init,
    headers,
    credentials: 'include',
  })

  if (!res.ok) {
    throw createApiError(res.status, await readJsonOrText(res))
  }
  return (await res.json()) as T
}
