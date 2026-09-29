---
id: TASK-001
title: "binding shape card"
type: chore
priority: P3
effort: S
status: todo
created: 2026-09-29
---

## Summary

Pins the three status-capture shapes: rc capture, propagated status, severed capture.

## Completion Criteria

- [ ] rc capture is recognized | verify: `grep -q alpha data.txt; rc=$?; test "$rc" -eq 0`
- [ ] propagated status is recognized | verify: `out=$(cat data.txt) || exit 1`
- [ ] severed capture is flagged | verify: `out=$(cat data.txt); grep -q alpha "$out"`
