// The Database form, described rather than written out.
//
// The coffee editor writes every input by hand, which is fine for a dozen
// fields. A Database has thirty, most of them the same two shapes, and hand
// writing them would be six hundred lines of template in which one wrong path
// string is invisible. So the fields are data, and one component renders them.
//
// The `help` lines are the CRD's own `description` text, shortened. That is
// deliberate: the platform team wrote them for exactly this reader, and a page
// that paraphrases them starts disagreeing with the API it edits.

import {
  COST_CENTRES,
  COST_CENTRE_HINTS,
  DATA_CLASSIFICATIONS,
  DELETION_POLICIES,
  ENGINES,
  EXPOSURES,
  REGIONS,
  REGION_HINTS,
  SIZES,
  SIZE_HINTS,
  TIERS,
} from './databaseTypes'

export type FieldKind =
  | 'text'
  | 'textarea'
  | 'number'
  | 'select'
  | 'checkbox'
  /** A comma-separated text input backed by an array of strings. */
  | 'list'

export interface FieldSpec {
  /** Dotted path under the object root, e.g. `spec.owner.team`. */
  path: string
  label: string
  kind: FieldKind
  options?: readonly string[]
  /** Second line beside an option, e.g. the city behind a region code. */
  hints?: Record<string, string>
  help?: string
  placeholder?: string
  /** Required by the CRD. Shown as a marker; the apiserver is what enforces it. */
  required?: boolean
  min?: number
  max?: number
  /** Renders across both columns of the grid. */
  wide?: boolean
}

export interface FieldGroup {
  title: string
  blurb?: string
  fields: FieldSpec[]
}

export const DATABASE_FORM: FieldGroup[] = [
  {
    title: 'What you are asking for',
    blurb:
      'Intent only: no instance classes, no subnet ids, no parameter groups. The platform team owns the translation into whatever the cluster’s cloud actually sells.',
    fields: [
      {
        path: 'spec.engine',
        label: 'Engine',
        kind: 'select',
        options: ENGINES,
        required: true,
        help: 'The platform team supports four.',
      },
      {
        path: 'spec.engineVersion',
        label: 'Engine version',
        kind: 'text',
        placeholder: '16',
        help: 'Major version. Leave it out and you get the current platform default — which means you also get the upgrade when the platform moves.',
      },
      {
        path: 'spec.tier',
        label: 'Tier',
        kind: 'select',
        options: TIERS,
        required: true,
        help: 'Drives replication, backup retention, patch window and who gets paged. This is the field the finance people actually read.',
      },
      {
        path: 'spec.size',
        label: 'Size',
        kind: 'select',
        options: SIZES,
        hints: SIZE_HINTS,
        help: 'T-shirt size, deliberately not vCPU and GiB: the mapping changes when the contract with the cloud changes.',
      },
      {
        path: 'spec.region',
        label: 'Region',
        kind: 'select',
        options: REGIONS,
        hints: REGION_HINTS,
        help: 'Where the data is allowed to live.',
      },
      {
        path: 'spec.storageGB',
        label: 'Storage override (GB)',
        kind: 'number',
        min: 20,
        max: 16384,
        help: 'Only set this if you know something the size table does not.',
      },
    ],
  },
  {
    title: 'Who is asking, and who pays',
    blurb: 'Not optional, not “TBD”, not a Slack handle.',
    fields: [
      {
        path: 'spec.owner.team',
        label: 'Owning team',
        kind: 'text',
        placeholder: 'payments-core',
        required: true,
        help: 'As it appears in the org directory. Lowercase, dash-separated.',
      },
      {
        path: 'spec.owner.costCentre',
        label: 'Cost centre',
        kind: 'select',
        options: COST_CENTRES,
        hints: COST_CENTRE_HINTS,
        required: true,
        help: 'If your cost centre is not in this list, it is not one the platform can charge, and the request stops here.',
      },
      {
        path: 'spec.owner.contact',
        label: 'Team mailing list',
        kind: 'text',
        placeholder: 'payments-core@example.com',
        required: true,
        help: 'A person will leave; the list will not.',
      },
      {
        path: 'spec.owner.onCallRotation',
        label: 'On-call rotation',
        kind: 'text',
        placeholder: 'PD-SCHED-1234',
        help: 'PagerDuty schedule id. Required for business-critical.',
      },
    ],
  },
  {
    title: 'What it is for',
    fields: [
      {
        path: 'spec.service',
        label: 'Service',
        kind: 'text',
        placeholder: 'checkout-api',
        required: true,
        help: 'Must match a service in the catalogue; this is how the CMDB finds the thing.',
      },
      {
        path: 'spec.dataClassification',
        label: 'Data classification',
        kind: 'select',
        options: DATA_CLASSIFICATIONS,
        help: 'The highest classification that will be stored. Drives encryption, audit logging, and whether the sandbox tier is allowed at all.',
      },
      {
        path: 'spec.purpose',
        label: 'Purpose',
        kind: 'textarea',
        wide: true,
        placeholder:
          'One or two sentences, written for the person who finds this in eighteen months.',
        help: 'Written for whoever finds it later and wonders whether deleting it will end their career.',
      },
    ],
  },
  {
    title: 'The boring parts that are never boring later',
    fields: [
      {
        path: 'spec.backup.schedule',
        label: 'Backup schedule',
        kind: 'text',
        placeholder: '0 2 * * *',
        help: 'Cron expression, UTC. Empty means “whatever the tier says”.',
      },
      {
        path: 'spec.backup.retentionDays',
        label: 'Backup retention (days)',
        kind: 'number',
        min: 0,
        max: 365,
      },
      {
        path: 'spec.backup.pointInTimeRecovery',
        label: 'Point-in-time recovery',
        kind: 'checkbox',
      },
      {
        path: 'spec.network.exposure',
        label: 'Exposure',
        kind: 'select',
        options: EXPOSURES,
        help: '“internet” needs a signed exception from Risk & Compliance, and you will be asked for it.',
      },
      {
        path: 'spec.network.allowedCIDRs',
        label: 'Extra source ranges',
        kind: 'list',
        placeholder: '10.0.0.0/8, 192.168.1.0/24',
        help: 'On top of the exposure setting. Comma-separated.',
      },
      {
        path: 'spec.maintenanceWindow',
        label: 'Maintenance window',
        kind: 'text',
        placeholder: 'Sun:02:00-04:00',
        help: 'Business-critical databases get patched in this window and nowhere else.',
      },
      {
        path: 'spec.deletionPolicy',
        label: 'Deletion policy',
        kind: 'select',
        options: DELETION_POLICIES,
        help: 'Yes, “delete” really deletes it. That is the point of writing it down where everyone can see it.',
      },
    ],
  },
]

/** Every path the form renders, in order. Used to check a new request before
 *  sending it, and to turn a path back into the label a person saw. */
export const DATABASE_FIELDS: FieldSpec[] = DATABASE_FORM.flatMap(
  (group) => group.fields,
)

/** The label this path was shown under, for the change summary. Falls back to
 *  the path so an unknown one is visible rather than blank. */
export function fieldLabel(path: string): string {
  return DATABASE_FIELDS.find((field) => field.path === path)?.label ?? path
}
