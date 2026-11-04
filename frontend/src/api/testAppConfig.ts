// For tests: the deployment settings loadAppConfig would fetch, loaded without
// touching whatever fetch stub the test has installed.
import { loadAppConfig, type AppConfig } from './appConfig'

export const TEST_CONFIG: AppConfig = {
  namespace: 'voter',
  coffeeConfigName: 'demo-coffee',
  roomName: 'demo',
  seatingPlanName: 'mission2',
  commitURLTemplate: '',
  settingsCommitURLTemplate: '',
  participantConnector: 'room-pass',
  gitTargetName: 'voter-demo',
  databaseGitTargetName: '',
  seatGitTargetName: '',
  settingsGitTargetName: '',
  commitRequestNamespace: '',
  // No lead: a test waits for nothing. kube.test covers the sleep itself.
  commitLeadMillis: 0,
  audienceCoffeeAdminRole: 'voter-audience-coffee-admin',
  audienceCoffeeKeysRole: 'voter-audience-coffee-keys',
  audienceBallotRole: 'voter-audience-ballot',
  audienceSeatClaimRole: 'voter-audience-seat-claim',
  audienceSeatEditRole: 'voter-audience-seat-edit',
  shopOnly: false,
  menuSource: 'kubernetes',
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
