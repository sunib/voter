# To do

Small things to come back to. Bigger plans live in [PLAN.md](PLAN.md).

## After the conference

- **Saving a key with the value it already has.** Today admission refuses it
  ("That is the mail key already set"), so nothing changes and no commit is made.
  Should it count as a real update instead, with a new commit and a new settings
  image? The catch: admission sees the merged result, so it cannot tell "same value"
  from an empty patch, and allowing those makes `patch` answer with the stored key,
  which turns it into `get`
  ([decision 5](docs/coffee-api-keys-design.md#5-admission-on-the-secrets-the-value-changes-and-nothing-else-does)).
  Any fix has to close that first.
