# Contributor guidance

This repository contains the public product, not private planning or live task data.
Use synthetic examples and tests. Keep private paths, credentials and boards out.

- Run `make check` before delivery; build artifacts belong under build/.
- CLI stdout is the requested data only; diagnostics go to stderr.
- File reads must not execute commands embedded in task content.
- Preserve source bytes for no-op operations. Parsing a view is not serialization.
- Add tests for malformed input and boundary cases alongside implementation.
- Do not claim full task lifecycle support while only codec and create/list are available.
- Work on separate task worktrees; preserve unrelated changes.
