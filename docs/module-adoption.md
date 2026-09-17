# 모듈 경로 채택

정책의 `modules`는 보드 바로 아래에서 카드 범위를 구분하는 명시적 루트입니다.
모듈은 workflow, kind, parking, archive 경로와 혼동되지 않는 소문자 이름이어야 합니다.

기존 보드에 모듈 경로가 이미 있거나 새 정책에 모듈을 추가할 때는 모든 writer가
모듈 인식 버전으로 업그레이드된 것을 확인한 뒤 최초 채택 요청에
`--adopt-modules`를 붙입니다.

```text
taskchain-task-manager activate-policy policy.yaml \
  --dir tasks --adopt-modules --json
```

활성 정책에 모듈 범위를 추가하는 revision도 같은 명시적 승인이 필요합니다.
모듈 범위가 그대로인 일반 revision은 기존 ID 원장을 변경하지 않습니다.

```text
taskchain-task-manager revise-policy policy-v3.yaml \
  --dir tasks \
  --expected-authority <현재 authority> \
  --expected-digest <현재 digest> \
  --adopt-modules --json
```

`--resume`은 최초 채택을 시작하지 않으며, 중단된 동일 요청의 원래 승인 범위를
그대로 사용해야 합니다. 공유 정책의 최초 채택·revision은 `--all-worktrees`도 요구합니다.
이미 활성화된 공유 정책에 새 worktree가 join하는 요청과는 구분합니다. 출력 실패 뒤에는
동일한 정책, authority, digest, 채택 플래그로 재시도하여 영속 receipt를 확인합니다.

모듈 카드는 선언된 모듈 아래의 workflow, kind, parking, archive 경로에서만 검색됩니다.
문서 파일은 카드로 검색하지 않으며 숨김·제외 경로, symlink와 모듈 직하 카드는
거부됩니다. 디렉터리 이름이 문서처럼 보인다는 이유만으로 그 아래 카드를 생략하지 않습니다.
