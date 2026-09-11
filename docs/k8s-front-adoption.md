# k8s-front adoption in Voter

Status: recommendation for planning; no implementation or deployment change authorized
by this document. The [product design](../k8s-front/README.md) and
[BFF decision guide](../k8s-front/bff-choice.md) remain independent of Voter.

## Recommendation

Pursue the reusable authentication/transport boundary as a separate product prototype.
Keep Voter's current service for the talk and prioritize the shared-stream capacity work
in [PLAN.md](../PLAN.md). Evaluate CoffeeConfig as the first integration candidate after
that work. Keep quiz operations in the domain backend until creation invariants and read
privacy have an implemented, tested replacement.

No second real consuming application is identified in the current proposal. Workspace and
reservation examples are illustrative, not evidence of adoption. The prototype is therefore
product development, not a demonstrated reduction in Voter's total maintenance cost.

## Apply the decision criteria

| Criterion | Voter assessment | Consequence |
| --- | --- | --- |
| User operations | Configuration editing fits resource access; quiz acceptance, voucher redemption and Git requests include domain rules | Evaluate each feature separately |
| Enforcement under arbitrary API calls | Quiz handler validation and server-chosen submission names are not admission enforcement | Keep raw QuizSubmission routes denied until equivalent guarantees exist |
| Read privacy | Participant grants include raw submission reads, while the results handler returns aggregates without resource metadata | Review effective grants and replace the read path before narrowing permissions or exposing raw access |
| Total implementation | Auth/transport can move out, but pricing, orders, validation and orchestration remain; a proxy and session store add work | Measure whole-system code and operations, not only Voter's line count |
| Failure handling | Conditional edits, uncertain creates and CoffeeConfig/CommitRequest partial success need explicit behavior | Preserve those tests and outcomes in any pilot |
| Expertise and ownership | Operator/admission, session storage and release ownership must be assigned for the new product | Name maintainers before implementation and deployment commitments |
| Reuse | A second actual consumer has not been identified | Require independent prototype evidence before claiming reuse savings |

## CoffeeConfig pilot gate

Preserve fixed-resource scope, spec-only edits, projection semantics, conditional writes and
late-response protection. Participant patch permissions alone do not encode all of these
restrictions. Keep CoffeeConfig save and CommitRequest orchestration together until their
partial-success behavior has a deliberate replacement. Do not open unrelated resource APIs
as a side effect of the pilot.

For quiz migration, first choose one stored object versus one accepted outcome per identity
and round. Then prove identity binding, concurrent requests, deletion/recreation behavior,
round-closing semantics and private results. Changing to controller acceptance is a domain
contract change, not a transport substitution. Narrowing submission read grants also breaks
the existing result handler using those credentials; coordinate the replacement.

The current [quiz handler](../voter/participant_quiz.go) already reads the round,
checks its state/version and then creates the submission in a separate operation:
the round can close or change between those steps. A transport migration by itself
neither introduces nor fixes this cross-object race; stricter guarantees require an
explicit domain protocol.

## Next decision and owners

The project owner decides whether to start the independent prototype. Domain maintainers
own any resource/admission redesign; platform maintainers own grants, session storage and
installation. After prototype evidence and the talk's capacity requirements are satisfied,
those owners decide whether a bounded CoffeeConfig pilot justifies its operational cost.
Until that decision, this recommendation does not change the accepted deployment plan.
