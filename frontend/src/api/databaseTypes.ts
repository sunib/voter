// The Database API, as the CRD defines it.
//
// The enum lists below are copies of the ones in
// voter/config/crd/databases.yaml, and they are copies on purpose: the form
// needs them to render a dropdown before the apiserver has been asked anything.
// They are NOT the validation. If one of these drifts from the CRD, the save is
// still refused by Kubernetes with the real list in the message -- which is the
// answer the room should see anyway.

import type { KubeObjectMeta } from './coffeeTypes'

export const ENGINES = ['postgresql', 'mysql', 'mariadb', 'sqlserver'] as const

/** Replication, backup retention, patch window and who gets paged. The field
 *  the finance people actually read. */
export const TIERS = ['sandbox', 'standard', 'business-critical'] as const

export const SIZES = ['small', 'medium', 'large', 'xlarge'] as const

/** What each t-shirt size buys, for the hint under the dropdown. The mapping is
 *  the platform team's business and lives in the CRD's comments; repeating it
 *  here is a label, not a contract. */
export const SIZE_HINTS: Record<string, string> = {
  small: '2 vCPU / 8 GiB / 100 GiB',
  medium: '4 vCPU / 16 GiB / 500 GiB',
  large: '8 vCPU / 32 GiB / 1000 GiB',
  xlarge: '16 vCPU / 64 GiB / 4000 GiB',
}

export const REGIONS = [
  'eu-central-2',
  'eu-central-1',
  'eu-west-1',
  'us-east-1',
] as const

export const REGION_HINTS: Record<string, string> = {
  'eu-central-2': 'Zurich',
  'eu-central-1': 'Frankfurt',
  'eu-west-1': 'Dublin',
  'us-east-1': 'N. Virginia',
}

/** If your cost centre is not in this list, it is not a cost centre the
 *  platform can charge, and the request stops here. */
export const COST_CENTRES = [
  'CC-finance-01',
  'CC-finance-04',
  'CC-finance-07',
  'CC-finance-12',
  'CC-finance-19',
  'CC-finance-23',
  'CC-finance-31',
] as const

export const COST_CENTRE_HINTS: Record<string, string> = {
  'CC-finance-01': 'Group Platform & Infrastructure',
  'CC-finance-04': 'Retail Banking',
  'CC-finance-07': 'Payments',
  'CC-finance-12': 'Data & Analytics',
  'CC-finance-19': 'Risk & Compliance',
  'CC-finance-23': 'Digital Channels',
  'CC-finance-31': 'Innovation (charged back, ask first)',
}

export const DATA_CLASSIFICATIONS = [
  'public',
  'internal',
  'confidential',
  'restricted',
] as const

export const EXPOSURES = [
  'cluster-only',
  'vpc',
  'corporate-network',
  'internet',
] as const

export const DELETION_POLICIES = [
  'retain',
  'snapshot-then-delete',
  'delete',
] as const

/** Pending through Failed. Empty for every object in this demo: nothing
 *  reconciles a Database, and that is the interesting part. */
export const PHASES = [
  'Pending',
  'Provisioning',
  'Ready',
  'Degraded',
  'Deleting',
  'Failed',
] as const

export type DatabaseOwner = {
  team: string
  costCentre: string
  contact: string
  onCallRotation?: string
}

export type DatabaseBackup = {
  schedule?: string
  retentionDays?: number
  pointInTimeRecovery?: boolean
}

export type DatabaseNetwork = {
  exposure?: string
  allowedCIDRs?: string[]
}

export type DatabaseSpec = {
  engine: string
  engineVersion?: string
  tier: string
  size?: string
  region?: string
  owner: DatabaseOwner
  service: string
  purpose?: string
  dataClassification?: string
  storageGB?: number
  backup?: DatabaseBackup
  network?: DatabaseNetwork
  maintenanceWindow?: string
  deletionPolicy?: string
}

export type DatabaseStatus = {
  phase?: string
  endpoint?: string
  port?: number
  credentialsSecretRef?: string
  observedGeneration?: number
}

export type Database = {
  apiVersion?: string
  kind?: string
  metadata?: KubeObjectMeta & { annotations?: Record<string, string> }
  spec: DatabaseSpec
  status?: DatabaseStatus
}

/** Where the backend records the note the requester typed into "why are you
 *  making this change?". Mirrors intentAnnotation in
 *  voter/participant_databases.go; the two must agree. */
export const INTENT_ANNOTATION = 'platform.configbutler.ai/intent'

/** The spec a brand-new request starts from: every CRD default filled in, every
 *  required field left empty so the form asks for it rather than inventing a
 *  team name nobody chose. */
export function emptyDatabaseSpec(): DatabaseSpec {
  return {
    engine: 'postgresql',
    tier: 'standard',
    size: 'small',
    region: 'eu-central-2',
    dataClassification: 'internal',
    deletionPolicy: 'snapshot-then-delete',
    service: '',
    owner: { team: '', costCentre: '', contact: '' },
    network: { exposure: 'cluster-only' },
  }
}

/** The sentence on the slide, filled in from the object. This is what the list
 *  page shows instead of a row of enum values: a request for a database is a
 *  sentence a person said, and it reads like one. */
export function intentSentence(spec: DatabaseSpec): string {
  // Built in three pieces rather than interpolated into one string, because a
  // half-filled form is the normal state of the create page and "The  team
  // needs a  database for their  service" is not a sentence. Each blank has its
  // own wording that still reads.
  const team = spec.owner?.team
    ? `The ${spec.owner.team} team`
    : 'A team not yet named'
  const service = spec.service
    ? `their ${spec.service} service`
    : 'a service not yet named'
  return `${team} needs a ${spec.engine || 'new'} database for ${service}.`
}
