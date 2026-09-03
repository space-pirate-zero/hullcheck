# Refusal manifest

The most valuable thing an unattended tool does is decline to proceed. Most tools
have never had that conversation with themselves. This is ours, enumerated and
reviewable like an API contract.

`hullcheck` stops, reports, and exits 2 when:

| Condition | Why it refuses rather than guessing |
|---|---|
| No policy documents found | A reading with no denominator has no meaning. Reporting 100% because we found nothing to check would be the exact failure this tool exists to expose. |
| The path is not a directory | Silently reading nothing and reporting a clean bill is worse than an error. |
| An unknown flag is passed | Ignoring a flag the user believed was doing something is a silent downgrade. |

It also declines to *claim* more than it proved:

- An unverified run is labelled a **READING, not a score**, on every single run. The
  default mode matched rules to gates by name and reference; it did not prove any gate
  fails when its rule is broken.
- A gate that nothing schedules is reported as `manual`, and counted as **never** in
  Time-to-Truth. It exists, it works, and it is not a detection latency.
- `FAKE` outranks `BREACH` in severity, because a gate that passes when its rule is
  broken manufactures confidence, and manufactured confidence is worse than a known gap.

**Hard stop, never a silent downgrade.**
