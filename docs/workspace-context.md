# Workspace 카드 묶음 조회

`workspace-context`는 workspace manifest에 명시한 여러 저장소를 한 번씩 훑고, 여러 카드
ID에 대해 저장소별 일치 결과 전체를 반환합니다. 보드 파일·잠금 디렉터리·Git 상태를
쓰지 않으며 카드 내용의 명령을 실행하지 않습니다. 저장소 경계 확인을 위해 실행하는
Git 명령은 로컬 정보를 읽기만 합니다. 기존 `query-workspace`의 정확히 하나인 결과 계약은
그대로 유지됩니다.

```sh
taskchain-task-manager workspace-context \
  --manifest workspace.json \
  --card-id TASK-90 \
  --card-id TASK-091 \
  --card-id ISSUE-12 \
  --json
```

manifest 형식과 저장소·보드 경계는 [`query-workspace`](workspace-query.md)의 manifest 계약을
따릅니다. manifest가 정한 보드만 읽으며 자동 발견하지 않습니다. 저장소 이름 오름차순으로
한 번씩 스캔하고 입력 ID 순서로 결과를 냅니다. `requestedId`는 요청 표기를 보존하고
`cardId`는 숫자 identity의 정규 표기입니다. `TASK-090`과 `TASK-90`처럼 같은 숫자 identity를
가진 ID를 한 요청에 중복해서 넣으면 오류입니다.

성공 JSON의 `outputVersion`은 `1`입니다. 각 결과의 `status`는 `missing`, `found`,
`ambiguous` 중 하나이며, `matches`에는 일치한 모든 저장소와 카드 view가 정렬되어
포함됩니다. 카드 내용의 명령·링크는 실행하지 않습니다. 하나라도 보드를 읽거나 검증하는
데 실패하면 stdout에 부분 JSON을 쓰지 않습니다.

실행 범위는 다음으로 제한됩니다.

- manifest의 저장소 1–32개
- 요청 ID 1–256개, ID당 최대 128 UTF-8 bytes
- 보드당 카드 최대 4,096개, 검사 노드 최대 8,192개, 파일 총량 최대 64 MiB

잘못된 카드 ID, symlink·중첩 Git metadata·보드 밖 경로, 중복/겹치는 물리 저장소 또는
Git common directory는 거부됩니다. 각 보드는 조회 전후 콘텐츠 digest와 파일 identity를
비교하고, Git 보드 경계도 다시 확인합니다. 조회 중 보드가 바뀌면 실패하므로 다시
시도해야 합니다. 이는 여러 저장소에 걸친 원자 snapshot이나 writer와의 lock 조정이
아닙니다. 동시 writer 실행을 멈춘 상태에서 사용하세요.

이 명령은 기존 보드 lock을 기다리거나 만들지 않습니다. 따라서 `query-workspace`처럼
writer와 조정되는 조회가 필요하면 [`query-workspace`](workspace-query.md)를 사용하세요.
