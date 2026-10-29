// The first thing a participant does after joining: take a seat.
//
// /join-room always lands on /, so the home page is where a participant who
// has no seat yet is sent on to /seat, once. Only participants: the operator
// has no seat to take. And only while there is a plan to take one on: with no
// SeatingPlan the room has nowhere to send them, and the quizzes stay open.

import { canVote, type Session } from './api/session'
import { findMySeat, getSeatingPlan } from './api/seats'
import { currentUsername } from './api/whoami'

// Once this browser has seen the person's seat, it stops asking: a seat is
// never deleted by its owner, so the answer does not go back.
let seated = false
// Sent once per page load, and never again: someone who goes back to the
// quizzes without a seat has chosen to, and a page that keeps sending them
// away would be a gate, not a first stop.
let offered = false

export function markSeated(): void {
  seated = true
}

/** Whether this person should take a seat before anything else. A failure to
 *  find out is a no: the redirect is a convenience, and must never be the
 *  reason someone cannot reach the quizzes. */
export async function needsSeat(session: Session | null): Promise<boolean> {
  if (seated || offered || session === null || !canVote(session)) return false
  try {
    if ((await findMySeat(await currentUsername())) !== null) {
      seated = true
      return false
    }
    offered = (await getSeatingPlan()) !== null
    return offered
  } catch {
    return false
  }
}
