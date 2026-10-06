// A shop-only deployment (SHOP_ONLY, /config.json `shopOnly`) is the coffee bar
// and nothing else: the production side of the demo, which Flux fills from Git
// and nobody edits. Kubernetes is what refuses an edit there; this only keeps
// the page from offering one.

import { appConfig } from './api/appConfig'

/** The routes a shop-only deployment serves: ordering, its thank-you page,
 *  signing in, and who you are signed in as. */
const SHOP_ROUTES = new Set(['order', 'thanks', 'login', 'me'])

export function shopOnly(): boolean {
  return appConfig().shopOnly === true
}

/** Where a shop-only deployment sends a route it does not serve, or null to
 *  let the navigation through. */
export function shopOnlyRedirect(
  routeName: string | symbol | null | undefined,
): { name: 'order' } | null {
  if (!shopOnly()) return null
  return SHOP_ROUTES.has(String(routeName ?? '')) ? null : { name: 'order' }
}
