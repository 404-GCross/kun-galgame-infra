# bangumi/common vocabulary snapshot

Embedded snapshot of the Bangumi shared vocabulary files, used to seed the
catalog registries (`catalog_role` base vocabulary, relation types, platform
mapping). The snapshot is pinned — it does not drift with upstream; refreshing
it is a deliberate act (re-download, update this README, re-run the tests,
adjust spot-check assertions if entries changed).

## Provenance

- Source repository: <https://github.com/bangumi/common>
- Upstream commit: `6a8442c17143a870357a5ff812362e8b5cfe9f9d` (committed 2025-11-12)
- Fetched: 2026-07-05, via
  `https://raw.githubusercontent.com/bangumi/common/6a8442c17143a870357a5ff812362e8b5cfe9f9d/<file>`

## Snapshot transformation (why these files differ byte-wise from upstream)

The upstream files build their payload out of a `define:` section full of YAML
anchors, and reference them with hundreds of aliases (whole subject-type
blocks like `2: *ANIME_STAFFS`). That trips go-yaml's hardcoded
"document contains excessive aliasing" billion-laughs guard, which cannot be
disabled. The snapshot is therefore stored **pre-expanded**: all aliases
resolved and the (then redundant) `define:` scaffolding dropped, via PyYAML

```python
doc = yaml.safe_load(raw)   # resolves all anchors/aliases
doc.pop("define", None)
yaml.safe_dump(doc, out, allow_unicode=True, sort_keys=True, default_flow_style=False)
```

The transformation is deterministic and content-preserving (entry counts and
values verified identical after expansion). Re-apply it when refreshing the
snapshot.

| File | Content | Entries at snapshot time |
|---|---|---|
| `subject_staffs.yml` | staff positions per subject type (1=book 2=anime 3=music 4=game 6=real) | 246 positions |
| `subject_relations.yml` | subject↔subject relation types per subject type | 67 relations |
| `subject_platforms.yml` | release platforms per subject type + `book_series` / `game_platforms` groups | 60 platforms |

## License

The bangumi/common repository declares **no license** as of the pinned commit
(no LICENSE file; README does not state one). The files are factual community
vocabulary data (ID→name tables) maintained by the Bangumi project and shared
across its official projects. Treat this snapshot as upstream-owned data:
attribution above, no relicensing, refresh from upstream only.
