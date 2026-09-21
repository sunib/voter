import type { KRMObject } from '@configbutler/krm-stream'
import { getAdminCoffeeConfig, patchAdminCoffeeConfig } from './coffee'
import type { CoffeeConfig } from './coffeeTypes'
import { useLiveEditableResource } from './liveEditableResource'

function coffeeResource(object: unknown): object is KRMObject & CoffeeConfig {
  if (!object || typeof object !== 'object') return false
  const resource = object as KRMObject
  return (
    resource.apiVersion === 'examples.configbutler.ai/v1alpha1' &&
    resource.kind === 'CoffeeConfig' &&
    typeof resource.metadata?.uid === 'string' &&
    typeof resource.metadata.resourceVersion === 'string' &&
    !!resource.spec &&
    typeof resource.spec === 'object' &&
    !Array.isArray(resource.spec)
  )
}

/** The coffee menu, live and editable. All of the draft, conflict, save and
 *  recovery mechanics are in liveEditableResource.ts, which the Database editor
 *  uses too -- this file is the three things that are specific to a
 *  CoffeeConfig: what one looks like, and how to read and write it. */
export function useLiveCoffeeConfig(
  namespace: string,
  name: string,
  editable = false,
) {
  return useLiveEditableResource<CoffeeConfig>({
    scope: {
      group: 'examples.configbutler.ai',
      version: 'v1alpha1',
      resource: 'coffeeconfigs',
      namespace,
      name,
    },
    editable,
    read: getAdminCoffeeConfig,
    write: (intent, reason) => patchAdminCoffeeConfig(intent, { reason }),
    isExpectedKind: coffeeResource,
    copy: {
      invalid: 'The stream returned an invalid CoffeeConfig.',
      replaced:
        'This configuration was removed or replaced. Open a new editor.',
      refreshed:
        'Configuration refreshed. Your edits are intact; review and save again.',
      alreadyDone:
        'Somebody else had already made this change. There is nothing left to save.',
      lostTheRace:
        'Somebody else is editing the menu too, and their save landed first each time. Your edits are kept — press Save again.',
    },
  })
}
