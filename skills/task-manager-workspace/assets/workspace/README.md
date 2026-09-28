# Synthetic workspace fixture

This bundle asset has two synthetic boards. `TASK-1` appears in both; `TASK-2`
exists only in `alpha`; `TASK-3` is absent. The manifest resolves repository
paths relative to itself. All cards and expected outputs are synthetic.

Require `taskchain-task-manager` and `git` on `PATH`. Set
`TASK_MANAGER_SKILL_DIR` to the directory containing this skill's `SKILL.md`
and `TASK_MANAGER_FIXTURE_DIR` to a fresh disposable destination. The commands
refuse an existing destination. Copy the asset before initializing its two Git
repositories; never initialize repositories inside an installed skill bundle.
The commands work from any current directory.

```sh
bundle_dir=${TASK_MANAGER_SKILL_DIR:?set the copied skill directory}
fixture_dir=${TASK_MANAGER_FIXTURE_DIR:?set a new disposable fixture path}
test -f "$bundle_dir/SKILL.md"
test ! -e "$fixture_dir"
cp -R "$bundle_dir/assets/workspace" "$fixture_dir"
git -C "$fixture_dir/repos/alpha" init -q
git -C "$fixture_dir/repos/beta" init -q

taskchain-task-manager workspace-context \
  --manifest "$fixture_dir/workspace.json" \
  --card-id TASK-1 --card-id TASK-2 --card-id TASK-3 --json \
  > "$fixture_dir/context.actual.json"
cmp "$bundle_dir/assets/workspace/expected-workspace-context.json" \
  "$fixture_dir/context.actual.json"

taskchain-task-manager query-workspace \
  --manifest "$fixture_dir/workspace.json" \
  --repository beta --card-id TASK-1 --json \
  > "$fixture_dir/query.actual.json"
cmp "$bundle_dir/assets/workspace/expected-query-beta.json" \
  "$fixture_dir/query.actual.json"
```

The first output labels `TASK-1` ambiguous, `TASK-2` found, and `TASK-3`
missing. The scoped query returns exactly beta's `TASK-1`. An unscoped
`query-workspace` for `TASK-1`, or any query for `TASK-3`, fails with nonzero
exit and no success JSON. `workspace-context` creates no board lock; the
writer-coordinated query removes its temporary lock before returning.
