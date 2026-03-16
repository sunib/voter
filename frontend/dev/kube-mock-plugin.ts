import type { Plugin, ViteDevServer } from 'vite'
import fs from 'node:fs'
import path from 'node:path'
import crypto from 'node:crypto'

type SessionFixture = {
  apiVersion: string
  kind: string
  metadata: { name: string; namespace?: string }
  spec: unknown
}

function parseCookies(header: string | null): Record<string, string> {
  if (!header) return {}
  const out: Record<string, string> = {}
  for (const part of header.split(';')) {
    const [k, ...rest] = part.trim().split('=')
    if (!k) continue
    out[k] = decodeURIComponent(rest.join('='))
  }
  return out
}

function readBody(req: any): Promise<string> {
  return new Promise((resolve) => {
    let data = ''
    req.on('data', (c: Buffer) => (data += c.toString('utf-8')))
    req.on('end', () => resolve(data))
  })
}

function json(res: any, status: number, body: unknown, headers?: Record<string, string>) {
  res.statusCode = status
  res.setHeader('content-type', 'application/json; charset=utf-8')
  if (headers) for (const [k, v] of Object.entries(headers)) res.setHeader(k, v)
  res.end(JSON.stringify(body, null, 2))
}

function text(res: any, status: number, body: string, headers?: Record<string, string>) {
  res.statusCode = status
  res.setHeader('content-type', 'text/plain; charset=utf-8')
  if (headers) for (const [k, v] of Object.entries(headers)) res.setHeader(k, v)
  res.end(body)
}

function ensureDir(p: string) {
  fs.mkdirSync(p, { recursive: true })
}

function setDevSessionCookie(res: any, cookieName: string) {
  const sid = crypto.randomBytes(24).toString('base64url')
  // NOTE: dev-only, not Secure.
  res.setHeader(
    'set-cookie',
    `${cookieName}=${encodeURIComponent(sid)}; Path=/; HttpOnly; SameSite=Lax; Max-Age=3600`,
  )
}

function shouldHandle(urlPath: string) {
  return urlPath.startsWith('/apis/examples.configbutler.ai/v1alpha1/')
}

function withAuthGate(_server: ViteDevServer, req: any, res: any): boolean {
  // Simulate ForwardAuth behavior locally:
  // - first request must include X-Join-Code
  // - on success, a device_session cookie is set
  // - subsequent requests rely on the cookie
  const cookieName = 'device_session'
  const cookies = parseCookies(req.headers.cookie ?? null)
  const hasCookie = Boolean(cookies[cookieName])
  if (hasCookie) return true

  const joinCode = String(req.headers['x-join-code'] ?? '').trim()
  if (!joinCode) {
    json(
      res,
      401,
      {
        message:
          'Missing device session. Provide X-Join-Code once (simulating forward-auth bootstrap).',
      },
      { 'x-dev-auth': 'missing' },
    )
    return false
  }

  setDevSessionCookie(res, cookieName)
  res.setHeader('x-dev-auth', 'bootstrapped')
  return true
}

export function kubeMockPlugin(): Plugin {
  return {
    name: 'present-yaml:kube-mock',
    configureServer(server) {
      server.middlewares.use(async (req, res, next) => {
        const url = req.url ? new URL(req.url, 'http://localhost') : null
        const pathname = url?.pathname ?? ''
        if (!pathname || !shouldHandle(pathname)) return next()

        // Handle preflight in case someone tests with a different origin.
        if (req.method === 'OPTIONS') {
          res.statusCode = 204
          res.setHeader('access-control-allow-origin', req.headers.origin ?? '*')
          res.setHeader('access-control-allow-credentials', 'true')
          res.setHeader('access-control-allow-methods', 'GET,POST,OPTIONS')
          res.setHeader(
            'access-control-allow-headers',
            (req.headers['access-control-request-headers'] as string) ?? 'content-type,x-join-code',
          )
          res.end()
          return
        }

        // Auth gate
        if (!withAuthGate(server, req, res)) return

        // Routes
        // GET /apis/examples.configbutler.ai/v1alpha1/namespaces/:ns/quizsessions/:name
        const mSession = pathname.match(
          /^\/apis\/examples\.configbutler\.ai\/v1alpha1\/namespaces\/([^/]+)\/quizsessions\/([^/]+)$/,
        )
        if (req.method === 'GET' && mSession) {
          const [, ns, name] = mSession
          const fixturesRoot = path.join(server.config.root, 'dev/fixtures/quizsessions')
          const file = path.join(fixturesRoot, `${decodeURIComponent(name)}.json`)

          if (!fs.existsSync(file)) {
            json(res, 404, { message: `No fixture for session ${name} in ${fixturesRoot}` })
            return
          }

          const raw = fs.readFileSync(file, 'utf-8')
          const parsed = JSON.parse(raw) as SessionFixture
          parsed.metadata = { ...(parsed.metadata ?? {}), name: decodeURIComponent(name), namespace: decodeURIComponent(ns) }
          json(res, 200, parsed)
          return
        }

        // POST /apis/examples.configbutler.ai/v1alpha1/namespaces/:ns/quizsubmissions
        const mSubmit = pathname.match(
          /^\/apis\/examples\.configbutler\.ai\/v1alpha1\/namespaces\/([^/]+)\/quizsubmissions$/,
        )
        if (req.method === 'POST' && mSubmit) {
          const [, ns] = mSubmit
          const bodyText = await readBody(req)
          let body: any
          try {
            body = JSON.parse(bodyText || '{}')
          } catch {
            json(res, 400, { message: 'Invalid JSON body' })
            return
          }

          const generateName = String(body?.metadata?.generateName ?? 'submission-')
          const name = `${generateName}${crypto.randomBytes(6).toString('hex')}`
          body.metadata = { ...(body.metadata ?? {}), name, namespace: decodeURIComponent(ns) }

          // File-based persistence for dev inspection.
          const dataDir = path.join(server.config.root, 'dev/data')
          ensureDir(dataDir)
          const outFile = path.join(dataDir, 'quizsubmissions.ndjson')
          fs.appendFileSync(outFile, `${JSON.stringify(body)}\n`, 'utf-8')

          json(res, 201, body)
          return
        }

        text(res, 404, `No mock handler for ${req.method} ${pathname}\n`)
      })
    },
  }
}
