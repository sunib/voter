import { describe, expect, it } from 'vitest'

import { commitURL, readCommitOutcome, shortSha } from './commitStatus'

// The real status of CommitRequest/database-save-pw52m, taken off the cluster
// on 2026-09-22. This is the object whose commit the editor claimed not to have
// observed, so it is the one the test is written against.
const committed = {
  status: {
    branch: 'main',
    sha: 'd82837fe657832ddf7f04c24b3a268eeaa16cd96',
    conditions: [
      {
        type: 'AuthorAttributed',
        status: 'True',
        reason: 'AttributedFromAdmission',
        message: 'the submitter was captured at admission and named as the commit author',
      },
      {
        type: 'Ready',
        status: 'True',
        reason: 'Committed',
        message: 'the open commit window was closed, committed, and pushed',
      },
      { type: 'Reconciling', status: 'False', reason: 'Committed' },
      { type: 'Stalled', status: 'False', reason: 'Committed' },
      {
        type: 'Pushed',
        status: 'True',
        reason: 'Pushed',
        message: 'the commit was pushed to the remote repository',
      },
    ],
  },
}

describe('readCommitOutcome', () => {
  it('reports a pushed commit, with the sha', () => {
    const outcome = readCommitOutcome(committed)
    expect(outcome.state).toBe('committed')
    expect(outcome.sha).toBe('d82837fe657832ddf7f04c24b3a268eeaa16cd96')
  })

  // The first event after a create has no conditions at all. Reading that as
  // anything but "still going" is how a working save reports a failure.
  it('treats an object with no conditions yet as pending', () => {
    expect(readCommitOutcome({ status: {} }).state).toBe('pending')
    expect(readCommitOutcome({}).state).toBe('pending')
    expect(readCommitOutcome(undefined).state).toBe('pending')
  })

  it('does not call Ready/False a commit', () => {
    const inflight = {
      status: {
        conditions: [
          { type: 'Ready', status: 'False', reason: 'Reconciling' },
          { type: 'Stalled', status: 'False' },
        ],
      },
    }
    expect(readCommitOutcome(inflight).state).toBe('pending')
  })

  it('reports a stall as a failure, and carries its reason', () => {
    const stalled = {
      status: {
        conditions: [
          { type: 'Ready', status: 'False' },
          {
            type: 'Stalled',
            status: 'True',
            message: 'the remote rejected the push',
          },
        ],
      },
    }
    const outcome = readCommitOutcome(stalled)
    expect(outcome.state).toBe('failed')
    expect(outcome.detail).toBe('the remote rejected the push')
  })

  // Stalled wins over a stale Ready: the last word about this object is that
  // ConfigButler gave up, and the screen must not show a green line above it.
  it('prefers a stall over a Ready left behind', () => {
    const both = {
      status: {
        conditions: [
          { type: 'Ready', status: 'True', reason: 'Committed' },
          { type: 'Stalled', status: 'True', message: 'gave up' },
        ],
      },
    }
    expect(readCommitOutcome(both).state).toBe('failed')
  })

  // A commit reported without a sha is still a commit. The screen drops the
  // "· 1234567" half rather than the sentence.
  it('survives a Ready with no sha', () => {
    const outcome = readCommitOutcome({
      status: { conditions: [{ type: 'Ready', status: 'True' }] },
    })
    expect(outcome.state).toBe('committed')
    expect(outcome.sha).toBe('')
  })
})

describe('shortSha', () => {
  it('is what a commit is called out loud', () => {
    expect(shortSha('d82837fe657832ddf7f04c24b3a268eeaa16cd96')).toBe('d82837f')
    expect(shortSha('')).toBe('')
  })
})

describe('commitURL', () => {
  const template =
    'https://github.com/ConfigButler/k8s-audit-trail/commit/{sha}'

  it('addresses the commit by its FULL sha, not the seven shown', () => {
    expect(commitURL(template, 'd82837fe657832ddf7f04c24b3a268eeaa16cd96')).toBe(
      'https://github.com/ConfigButler/k8s-audit-trail/commit/d82837fe657832ddf7f04c24b3a268eeaa16cd96',
    )
  })

  // No template configured, or a commit with no sha: the screen falls back to
  // plain text. A link that 404s argues against the very claim the line makes.
  it('returns nothing to link to when it cannot build a real link', () => {
    expect(commitURL('', 'd82837fe')).toBe('')
    expect(commitURL(template, '')).toBe('')
    expect(commitURL('', '')).toBe('')
  })
})
