# bangumi/wiki-syntax-spec test-case snapshot

Shared conformance cases for the Bangumi infobox wiki syntax, used by
`spec_test.go`. Pinned snapshot — refreshing it is a deliberate act (update
this README, re-run the suite).

- Source repository: <https://github.com/bangumi/wiki-syntax-spec>
- Upstream commit: `fe7435e425469184337b99b35b190548bf5e9cfa` (committed 2022-02-13)
- Fetched: 2026-07-06
- Layout: `valid/*.wiki` + `valid/*.yaml` (input + expected parse),
  `invalid/*.wiki` (must fail to parse).

Compatibility note: the in-house parser's behavior baseline is the reference
implementation (wiki-parser-go v0.0.2), verified by full-dump differential
testing — NOT the spec text. If a spec case ever conflicts with the reference
behavior, the reference wins and the conflict is documented in the step-08
execution report (none were found at snapshot time).
