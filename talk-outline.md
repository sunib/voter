# Session Submission Template

## Session Title
The GitOps Paradox: Why Your Devs Need an API You Don't Want to Build

## Alternative Titles:

1. **GitOps Needs an API**
2. **An API for Your GitOps Folder**
3. **What GitOps Can Learn from KRM**
4. **Combining the Impossible: A GUI, KRM, an API, and GitOps**
5. **The GitOps Paradox: Why Your Devs Need an API You Don't Want to Build**
6. **API-First GitOps: Building a Declarative Interface for Your Platform**
7. **From YAML Hell to Happy Devs: A KRM-Based API for Your Platform**
8. **Your Platform's Product is its API: A KRM-Native Approach**
9. **Unlocking True Self-Service: An API-First Pattern for Platform Teams**

## Description (Max 1000 chars)
Platform teams face a paradox: developers need simple APIs and UIs, but building and maintaining them is a huge operational burden that pulls teams away from core platform work. 

This session coins "Reverse GitOps," an API-first pattern that solves this by placing a light Kubernetes control plane in front of your Git repository. We'll show how platform teams can define simple CRDs that act as a declarative API for their platform, which can then be used to build GUIs (Backstage/Headlamp) or to allow devs and AI agents to interact with the platform programmatically. 

When a user interacts with this API, the open-source `gitops-reverser` operator translates their high-level intent into a clean Git commit. This commit creates a pull request, preserving your existing review process. Once merged, this simple CR can be "exploded" into a full set of production-ready manifests by tools like KRO or Crossplane, integrating with your existing GitOps workflow powered by tools Flux or ArgoCD.


## Benefits to the Ecosystem
This talk moves beyond a simple demo to investigate the real-world consequences of an API-first GitOps model. It provides the ecosystem with a concrete architectural pattern for building true self-service platforms, clarifying the crucial distinction between a lightweight **Control Plane** (for high-level user intent) and production **Data Planes**.

Crucially, this session will honestly confront the "source of truth" paradox this pattern creates. We will discuss the trade-offs of demoting Git from the single write-path to the canonical "source of record," and explore the engineering required to handle consistency, failure modes, and secure secret management.

On a personal note, I believe so strongly that the community needs this open-source primitive that I've left my full-time job to build it. By accepting this talk, you are platforming a foundational pattern from an independent builder dedicated to solving this problem in the open. This session will spark debate and give platform engineers a powerful new pattern for building more inclusive and resilient platforms.

## Is This a Case Study?
No

## Have You Presented This Talk Before?
No

## Open Source Projects Used
Kubernetes, Flux, ArgoCD, KRM, Headlamp, Helm, Kustomize, KRO (Kubernetes Resource Operator)

## Additional Resources
[Link to YouTube video demonstrating KRM interfaces and Reverse GitOps in action - to be created]

## Session Format
Presentation

## Level
Intermediate

## Target Audience
Platform engineers, SREs, and infrastructure architects responsible for building and operating internal developer platforms. This talk is for those who want to provide a better developer experience (APIs, GUIs) without sacrificing the safety and auditability of their existing GitOps workflows.

## Speaker(s) Info
- Name: Simon Koudijs
- Email: simon@configbutler.ai
- Title: Founder
- Company: ConfigButler
- Bio: Expert in configuration management and GitOps, open-sourcing tools to simplify SaaS settings and infrastructure while pioneering reverse GitOps paradigms.
- Country: Netherlands
- Diversity Info (Optional): Male
- Social/Fediverse: @

## Co-Speakers (If Applicable)
None

## Commitments
- [x] Reviewed CNCF Code of Conduct
- [x] Reviewed LF Inclusive Speaker Orientation
- [x] Can present if selected for multiple
- [x] Under submission limits