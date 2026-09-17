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
문서 파일과 숨김·제외 디렉터리는 카드 검색 대상에서 제외합니다. 검색 대상의 symlink와
모듈 직하 카드는 거부합니다. 쓰기 목적지에는 숨김·제외 경로도 허용하지 않습니다.
디렉터리 이름이 문서처럼 보인다는 이유만으로 그 아래 카드를 생략하지 않습니다.

## 모듈에 카드 생성

```text
taskchain-task-manager create --dir tasks --module backend \
  --category auth/api --title "Session renewal" --json
```

TASK는 `backend/todo/auth/api/ID.md`, 다른 kind는 해당 kind zone에 생성됩니다.
module을 생략한 기존 생성 경로는 유지합니다. category만 지정할 수는 없습니다.
경로 구성요소는 최대 255 bytes, module 카드 상대 경로는 최대 1023 bytes입니다.
숨김·제외·traversal·zone과 혼동되는 category와 symlink ancestor는 거부합니다.

`create-bundle` 요청의 각 task에도 선택적 `module`, `category` 문자열을 지정할 수 있습니다.
두 필드를 생략하면 기존 canonical 요청 bytes와 digest를 보존합니다. 명시한 빈 문자열은
생략과 달리 요청에 남으므로, 중단 후에는 원래 요청을 그대로 사용해야 합니다.
목적지는 영속 계획에 고정하며, 완료 영수증 재조회는 카드를 다시 생성하지 않습니다.

## 이동과 상태 복구

모듈의 상태 전이와 kind relocation은 module/category/filename을 보존합니다.
목적지가 경로 한도를 초과하면 이동을 시작하지 않습니다. `repair-status`는 선언된
workflow zone의 TASK만 복구하며 kind·archive 경로를 완료 상태로 해석하지 않습니다.
복구 기록은 당시 정책과 digest를 보존합니다. 중단된 복구는 동일 정책과 요청으로
재개하고, 완료 기록은 이후 정책 revision이나 카드 삭제가 있어도 재실행하지 않습니다.

모듈 복구 기록을 모르는 이전 버전은 해당 보드의 기록을 읽거나 다시 쓰지 못합니다.
공유 worktree에서 복구가 진행 중이면 다른 writer도 차단됩니다. 완료 후에는 다른
worktree의 기존 작업을 불필요하게 차단하지 않습니다. 업그레이드는 항상 모든 writer를
대상으로 진행하고, pending 기록을 삭제하여 구버전 쓰기를 강제로 허용하지 마세요.
