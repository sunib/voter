# Architecture

The current [Voter and Room Pass architecture](room-pass/advised_architecture.md)
describes the shared Dex issuer, backend OIDC session and participant-token
Kubernetes requests.

See [authorization and tests](docs/authorization.md) for who may do what and
[state-of-the-repo.md](state-of-the-repo.md) for implementation gaps.

The former ForwardAuth/TokenRequest architecture has been retired. Its design
history remains in Git and the historical documents indexed in [docs](docs/README.md).
