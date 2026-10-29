import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { createApiError } from './http'
import { seatGrid } from './liveSeats'
import {
  SEAT_EMOJIS,
  buildSeat,
  changeSeat,
  checkWhy,
  claimSeat,
  findMySeat,
  pickRandom,
  seatErrorMessage,
  seatName,
} from './seats'
import { useTestAppConfig } from './testAppConfig'

// The enum in voter/config/crd/seats.yaml, which the API server holds; the
// phone must offer exactly these.
const ALICE = { displayName: 'Alice', username: 'demo:CgVhbGljZQ' }
const BOB = { displayName: 'Bob', username: 'demo:CgNib2I' }

const CRD_EMOJIS = ["🦊","🐙","🦄","🐝","🐢","🦉","🐳","🦖","🐧","🦀","🐸","🐼",
  "🚀","🌵","🍄","🌈","⚡","🔥","🍕","🧇","☕","🎸","🎲","👾"]

describe('a seat', () => {
  beforeEach(async () => {
    await useTestAppConfig({ seatingPlanName: 'mission2', seatGitTargetName: 'seats' })
  })

  it('is named after its plan and place', () => {
    expect(seatName('mission2', 3, 7)).toBe('mission2-3-7')
  })

  it('is labelled as admission requires: padded row and column', () => {
    const seat = buildSeat('mission2', 3, 17, ALICE, '#e4572e', '🦊')
    expect(seat.metadata).toMatchObject({
      name: 'mission2-3-17',
      namespace: 'voter',
      labels: {
        'voter.configbutler.ai/seatingplan': 'mission2',
        'voter.configbutler.ai/row': '03',
        'voter.configbutler.ai/column': '17',
      },
    })
    expect(Object.keys(seat.metadata.labels ?? {})).toHaveLength(3)
    expect(seat.spec).toEqual({
      seatingPlanRef: { name: 'mission2' },
      row: 3,
      column: 17,
      claimedBy: ALICE,
      color: '#e4572e',
      emoji: '🦊',
    })
  })

  it('offers exactly the emojis the CRD accepts', () => {
    expect([...SEAT_EMOJIS]).toEqual(CRD_EMOJIS)
  })

  it('picks from the whole list, never past its end', () => {
    expect(pickRandom(['a', 'b'], () => 0)).toBe('a')
    expect(pickRandom(['a', 'b'], () => 0.9999)).toBe('b')
  })
})

describe('checkWhy', () => {
  it('requires a why', () => {
    expect(checkWhy('')).not.toBe('')
    expect(checkWhy('   ')).not.toBe('')
  })

  it('takes 3 to 80 characters once trimmed', () => {
    expect(checkWhy('  ok  ')).not.toBe('')
    expect(checkWhy('yes')).toBe('')
    expect(checkWhy('x'.repeat(80))).toBe('')
    expect(checkWhy('x'.repeat(81))).not.toBe('')
  })

  it('counts an emoji once, as admission does', () => {
    expect(checkWhy('🦊'.repeat(80))).toBe('')
  })
})

describe('seatGrid', () => {
  it('draws row 1 first, and leaves out a seat outside the plan', () => {
    const inside = buildSeat('mission2', 2, 3, ALICE, '#e4572e', '🦊')
    const outside = buildSeat('mission2', 9, 1, BOB, '#e4572e', '🐙')
    const grid = seatGrid({ rows: 2, columns: 3 }, [inside, outside])
    expect(grid).toHaveLength(2)
    expect(grid[0]).toHaveLength(3)
    expect(grid[0]?.[0]).toMatchObject({ row: 1, column: 1, seat: undefined })
    expect(grid[1]?.[2]?.seat).toBe(inside)
    expect(grid.flat().filter((cell) => cell.seat)).toHaveLength(1)
  })
})

describe('seatErrorMessage', () => {
  it('says a seat was taken in plain words', () => {
    const err = createApiError(409, { reason: 'AlreadyExists', message: 'x' })
    expect(seatErrorMessage(err)).toBe(
      'Someone just took that seat. Pick one close by.',
    )
  })

  it("shows the policy's own sentence", () => {
    const err = createApiError(422, {
      reason: 'Invalid',
      message:
        "seats \"mission2-9-1\" is invalid: : ValidatingAdmissionPolicy 'voter-seat-in-plan' with binding 'voter-seat-in-plan' denied request: This room has 8 rows of 12 seats. Pick one on the map.",
    })
    expect(seatErrorMessage(err)).toBe(
      'This room has 8 rows of 12 seats. Pick one on the map.',
    )
  })

  it('says the plan is missing, not that a binding has no params', () => {
    const err = createApiError(422, {
      message:
        "ValidatingAdmissionPolicy 'voter-seat-in-plan' with binding 'voter-seat-in-plan' denied request: failed to configure binding: no params found for policy binding with `Deny` parameterNotFoundAction",
    })
    expect(seatErrorMessage(err)).toMatch(/seating plan isn't set up yet/)
  })

  it('turns an RBAC refusal into a sentence', () => {
    const err = createApiError(403, {
      reason: 'Forbidden',
      message:
        'seats.examples.configbutler.ai "mission2-1-1" is forbidden: User "demo:x" cannot create resource "seats"',
    })
    expect(seatErrorMessage(err)).toBe("You're not allowed to do that here.")
  })
})

describe('writing a seat', () => {
  type Call = { url: string; method: string; body?: unknown }
  let calls: Call[]

  function stubFetch(answer: (call: Call) => Response) {
    calls = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const call = {
          url,
          method: init?.method ?? 'GET',
          body: init?.body ? JSON.parse(String(init.body)) : undefined,
        }
        calls.push(call)
        return answer(call)
      }),
    )
  }
  const json = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), {
      status,
      headers: { 'content-type': 'application/json' },
    })

  beforeEach(async () => {
    await useTestAppConfig({ seatingPlanName: 'mission2', seatGitTargetName: 'seats' })
  })
  afterEach(() => vi.unstubAllGlobals())

  it('claims, then passes the trimmed why on to the seats target', async () => {
    stubFetch((call) =>
      json(call.method === 'POST' ? { ...(call.body as object), metadata: { name: 'made' } } : {}),
    )
    const receipt = await claimSeat(3, 7, ALICE, '#e4572e', '🦊', '  by the door ')
    expect(receipt.whyLost).toBe(false)
    expect(calls.map((c) => [c.method, c.url])).toEqual([
      ['POST', '/k8s/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/seats?fieldManager=voter'],
      ['POST', '/k8s/apis/configbutler.ai/v1alpha3/namespaces/voter/commitrequests?fieldManager=voter'],
    ])
    expect(calls[1]?.body).toMatchObject({
      metadata: { generateName: 'seat-claim-' },
      spec: { gitTargetRef: { name: 'seats' }, message: 'by the door' },
    })
  })

  it('sends nothing else when the claim is refused', async () => {
    stubFetch(() => json({ reason: 'AlreadyExists', message: 'exists' }, 409))
    await expect(
      claimSeat(3, 7, ALICE, '#e4572e', '🦊', 'by the door'),
    ).rejects.toMatchObject({ status: 409 })
    expect(calls).toHaveLength(1)
  })

  it('reports a why that could not be passed on, not a failure', async () => {
    stubFetch((call) =>
      call.url.includes('commitrequests') ? json({ message: 'no' }, 500) : json({}),
    )
    const receipt = await claimSeat(3, 7, ALICE, '#e4572e', '🦊', 'by the door')
    expect(receipt.whyLost).toBe(true)
  })

  it('changes colour and emoji, and nothing else', async () => {
    stubFetch(() => json({ metadata: { name: 'made' } }))
    const seat = buildSeat('mission2', 3, 7, ALICE, '#e4572e', '🦊')
    await changeSeat(seat, '#2ec4b6', '🐙', 'teal is calmer')
    expect(calls[0]).toMatchObject({
      method: 'PATCH',
      url: '/k8s/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/seats/mission2-3-7?fieldManager=voter',
      body: { spec: { color: '#2ec4b6', emoji: '🐙' } },
    })
    expect(calls[1]?.body).toMatchObject({
      metadata: { generateName: 'seat-change-' },
      spec: { message: 'teal is calmer' },
    })
  })

  it('asks the API server for your seat by username, on the active plan only', async () => {
    const other = buildSeat('elsewhere', 1, 1, ALICE, '#e4572e', '🦊')
    const mine = buildSeat('mission2', 3, 7, ALICE, '#e4572e', '🦊')
    stubFetch(() =>
      json({ apiVersion: 'examples.configbutler.ai/v1alpha1', kind: 'SeatList', items: [other, mine] }),
    )
    expect((await findMySeat(ALICE.username))?.metadata.name).toBe('mission2-3-7')
    const url = new URL(calls[0]?.url ?? '', 'https://app.test')
    expect(url.pathname).toBe('/k8s/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/seats')
    expect(url.searchParams.get('fieldSelector')).toBe('spec.claimedBy.username=demo:CgVhbGljZQ')
  })

  it('takes any username, escaping what a field selector reads as syntax', async () => {
    stubFetch(() =>
      json({ apiVersion: 'examples.configbutler.ai/v1alpha1', kind: 'SeatList', items: [] }),
    )
    expect(await findMySeat('github:simon koudijs@example.com')).toBeNull()
    await findMySeat('github:a=b,c\\d')
    const asked = calls.map((c) => new URL(c.url, 'https://app.test').searchParams.get('fieldSelector'))
    expect(asked).toEqual([
      'spec.claimedBy.username=github:simon koudijs@example.com',
      'spec.claimedBy.username=github:a\\=b\\,c\\\\d',
    ])
  })

  it('has no seat to find without a username', async () => {
    stubFetch(() => json({}, 500))
    expect(await findMySeat('')).toBeNull()
    expect(calls).toHaveLength(0)
  })
})
