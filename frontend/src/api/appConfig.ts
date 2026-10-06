// Where this deployment keeps its objects.
//
// Voter serves these at /config.json, rendered from its environment, before
// anyone signs in. They used to arrive with the session, but the session is
// krm-foyer's now, and krm-foyer knows nothing about this application.

export interface AppConfig {
  /** Where this application's objects live. */
  namespace: string
  coffeeConfigName: string
  /** The Room whose join code the operator page renders. */
  roomName: string
  /** Turns a commit sha into a link, with "{sha}" substituted. Empty when the
   *  deployment has not been told where its audit trail is readable, in which
   *  case a sha is shown as plain text -- a dead link would be worse. */
  commitURLTemplate: string
  /** The Dex connector whose logins are the audience, and so may vote. */
  participantConnector: string
  /** The ConfigButler GitTarget a CoffeeConfig save asks to commit; empty for
   *  none. */
  gitTargetName: string
  /** The same for a Database save; empty until a target watches Databases. */
  databaseGitTargetName: string
  /** Where CommitRequests are created; empty means `namespace`. */
  commitRequestNamespace: string
  /** How long a commit window may wait for the write it publishes. */
  commitCloseDelaySeconds: number
  /** The Role the operator binds the audience to, which names the binding too. */
  audienceCoffeeAdminRole: string
  /** The Role the operator binds a group to so it may vote. Each binding is
   *  named after it and the group (authz.ts, ballotBindingName). */
  audienceBallotRole: string
}

let loaded: AppConfig | null = null

/** Fetched once, before the application mounts (main.ts), so every screen can
 *  read it synchronously. */
export async function loadAppConfig(): Promise<AppConfig> {
  const res = await fetch('/config.json', {
    headers: { accept: 'application/json' },
  })
  if (!res.ok) {
    throw new Error(`could not load /config.json: ${res.status}`)
  }
  loaded = (await res.json()) as AppConfig
  return loaded
}

/** The configuration loadAppConfig fetched. Throws before that, which is a
 *  bug in the startup order rather than something a screen should handle. */
export function appConfig(): AppConfig {
  if (loaded === null) {
    throw new Error('appConfig() called before loadAppConfig()')
  }
  return loaded
}
