---
id: TASK-001
title: "real card with quoted fixture id"
type: chore
priority: P3
effort: S
status: todo
created: 2026-09-29
---

## Summary

The body quotes an old probe card whose id must not claim a number.

## Completion Criteria

- [ ] bound | verify: `test -f absent-floor-marker.txt`

Reproduction block quoted from an old issue:

```bash
printf -- '---\nid: TASK-999\ntitle: "probe"\n---\n' > "$SP/99-probe.md"
```
