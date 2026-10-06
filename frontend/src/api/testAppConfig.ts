// For tests: the deployment settings loadAppConfig would fetch, loaded without
// touching whatever fetch stub the test has installed.
import { loadAppConfig, type AppConfig } from './appConfig'

export const TEST_CONFIG: AppConfig = {
  namespace: 'voter',
  coffeeConfigName: 'demo-coffee',
  roomName: 'demo',
  commitURLTemplate: '',
  participantConnector: 'room-pass',
  gitTargetName: 'voter-demo',
  databaseGitTargetName: '',
  commitRequestNamespace: '',
  commitCloseDelaySeconds: 2,
  audienceCoffeeAdminRole: 'voter-audience-coffee-admin',
  audienceBallotRole: 'voter-audience-ballot',
}

export async function useTestAppConfig(
  overrides: Partial<AppConfig> = {},
): Promise<void> {
  const original = globalThis.fetch
  globalThis.fetch = (async () =>
    new Response(
      JSON.stringify({ ...TEST_CONFIG, ...overrides }),
    )) as typeof fetch
  try {
    await loadAppConfig()
  } finally {
    globalThis.fetch = original
  }
}
