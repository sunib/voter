// The live order feed.
//
// This is the smallest live binding in the app, and the contrast is the point.
// liveCoffeeConfig.ts and liveResources.ts go through @configbutler/krm-stream:
// a shared Kubernetes watch, per-subscriber authorization, resourceVersion
// bookkeeping, drafts and conflicts. All of that exists because the thing on
// the other end is a Kubernetes object that several people may edit at once.
//
// An order is not one. It is an append-only event in the Voter process, so this
// is a plain EventSource over a plain SSE endpoint, and the whole state is an
// array plus the sequence numbers already seen. Nothing to reconcile, nothing
// to merge, and -- deliberately -- nothing in Git.
import { onScopeDispose, ref, shallowRef } from 'vue'

import type { CoffeeOrderRecord, CoffeeOrdersSnapshot } from './coffeeTypes'

/** How the browser is currently getting orders. */
export type OrderFeedStatus = 'connecting' | 'live' | 'retrying' | 'refused'

/** Watches the in-memory order feed and keeps `orders` newest-first. */
export function useLiveOrders() {
  const orders = shallowRef<CoffeeOrderRecord[]>([])
  const status = ref<OrderFeedStatus>('connecting')
  const error = ref('')
  const scope = ref('')
  const capacity = ref(0)

  // The stream replays its snapshot to every new connection, and a reconnect
  // replays it again. Without this, a dropped connection would duplicate the
  // whole list on screen.
  const seen = new Set<number>()

  let source: EventSource | undefined
  let closed = false

  function accept(record: CoffeeOrderRecord) {
    if (seen.has(record.seq)) {
      return
    }
    seen.add(record.seq)
    // Newest first: the screen is watched from the top, and an order arriving
    // below the fold during a demo is an order nobody saw.
    orders.value = [record, ...orders.value].sort((a, b) => b.seq - a.seq)
  }

  function connect() {
    if (closed) {
      return
    }
    source?.close()
    const stream = new EventSource('/public/orders/stream')
    source = stream

    stream.onopen = () => {
      status.value = 'live'
      error.value = ''
    }
    stream.onmessage = (event) => {
      try {
        accept(JSON.parse(event.data) as CoffeeOrderRecord)
      } catch {
        // A frame we cannot read is one order missed, not a reason to tear
        // down a feed that is otherwise working.
      }
    }
    stream.onerror = () => {
      // EventSource reconnects by itself, and says nothing about why it
      // failed -- a refused subscription and a dropped wire look identical
      // here. So this reports the retry rather than inventing a cause; the
      // snapshot fetch below is what can actually explain a refusal.
      if (stream.readyState === EventSource.CLOSED) {
        status.value = 'refused'
        error.value = 'The live feed closed. Reload to try again.'
        return
      }
      status.value = 'retrying'
    }
  }

  /** The first paint, and the only call that can report WHY the feed refused. */
  async function loadSnapshot() {
    try {
      const res = await fetch('/public/orders', {
        credentials: 'include',
        cache: 'no-store',
      })
      if (!res.ok) {
        status.value = 'refused'
        error.value = `The order feed answered ${res.status}.`
        return
      }
      const snapshot = (await res.json()) as CoffeeOrdersSnapshot
      scope.value = snapshot.scope
      capacity.value = snapshot.capacity
      for (const record of snapshot.orders) {
        accept(record)
      }
    } catch (e) {
      status.value = 'refused'
      error.value = e instanceof Error ? e.message : 'The order feed is unreachable.'
    }
  }

  function stop() {
    closed = true
    source?.close()
    source = undefined
  }

  void loadSnapshot().then(connect)
  onScopeDispose(stop)

  return { orders, status, error, scope, capacity, stop }
}
