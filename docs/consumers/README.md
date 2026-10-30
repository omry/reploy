# Consumer requirements and progress

Record concrete consumer needs here. Start with one document per consumer; split
it into a subdirectory only when that improves navigation.

| Consumer | Current need | Status | Next step |
| --- | --- | --- | --- |
| [OmegaFlow](omegaflow.md) | Public preparation inspection and admission of the inspected workload; separate private-endpoint dependency | Requirements/HLD approved; LLD unfinished and paused for E | Resume detailed engineering when prioritized |
| [Dingo](dingo.md) | Development workspace and robot deployment capabilities | Historical needs recorded; current gaps need revalidation | Evaluate and prioritize against current Reploy behavior |

## What belongs here

Each consumer document should describe its use cases, requirements and acceptance
checks, current support and gaps, delivery decisions and explicit deferrals, and
progress with links to designs, backlog items, PRs and qualified releases.
Distinguish consumer-reported needs, verified behavior and proposed solutions.
Date observations and retain their source and limitations. An implementation or
approved PR does not by itself prove consumer acceptance on a released version.

Keep shared Reploy capability designs in their existing locations and link to
them. Consumer documents explain why a capability is needed and whether delivery
unblocks that consumer; they do not create a second specification for it.

Use [the backlog](../BACKLOG.md) for priority and the next action. Where a consumer
document owns the requirements, link to it instead of repeating a large
requirements list in the backlog. Keep consumer-specific policy with its owner,
and link multiple consumers to the same shared capability rather than duplicating
delivery work.
