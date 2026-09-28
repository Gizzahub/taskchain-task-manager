# Workspace 조회

`query-workspace`는 명시한 저장소들의 작업 보드를 논리적으로 읽기 전용 조회합니다. 보드
writer와 조정하기 위해 조회 중 각 보드에 공통 writer lock과 임시 `.task-manager.lock`을
만들었다가 제거합니다. 저장소나
보드를 자동 발견하지 않고, 카드 내용의 명령·링크를 실행하거나 추가 파일을 선택하지
않습니다.

## 입력

다음 JSON 파일을 사용합니다. `path`는 이 파일이 있는 디렉터리 기준 저장소 경로,
`board`는 저장소 기준 작업 보드 경로입니다.

```json
{
  "schemaVersion": 1,
  "repositories": [
    {"name": "backend", "path": "repos/backend", "board": "tasks"},
    {"name": "frontend", "path": "repos/frontend", "board": "tasks"}
  ]
}
```

저장소 이름은 고유한 소문자 kebab 표기입니다. 경로는 정리된 상대 경로여야 하며
절대 경로, 부모 탐색, 심볼릭 링크와 같은 경로 우회는 거부합니다. 같은 저장소·보드를
다른 이름으로 중복 선언할 수 없고 보드 경로끼리 포함 관계여도 거부합니다. 경로
구성 요소의 대소문자도 실제 디렉터리 항목과 일치해야 합니다. 알 수 없는 JSON
필드와 중복 키도 오류입니다.
이 입력은 제품 자체의 공개 계약이며 devbox의 `.gz-git.yaml`을 읽지 않습니다.

## 조회

```sh
taskchain-task-manager query-workspace --manifest workspace.json --card-id TASK-90 --json
taskchain-task-manager query-workspace --manifest workspace.json --repository backend --card-id TASK-090 --json
taskchain-task-manager query-workspace --manifest workspace.json --kind intent --context-id INTENT-0123456789abcdef0123456789abcdef --revision 1 --json
```

카드 ID는 숫자 identity로 비교하므로 `TASK-090`과 `TASK-90`은 같은 카드입니다.
카드 결과는 저장소 이름과 기존 카드 목록의 작은 view를 돌려줍니다. 등록 context는
정확한 종류·ID·revision 하나를 조회하며, 기존 등록 결과의 canonical 문서와 digest를
돌려줍니다. 카드를 Intent/Batch에 자동 연결하거나 최신 revision을 추측하지 않습니다.
성공 stdout의 최상위 `outputVersion`은 `1`입니다. manifest와 반환된 canonical 문서는
각각의 저장 형식에 따른 `schemaVersion`을 유지합니다.

선택한 범위에서 결과가 없거나 둘 이상의 저장소가 같은 ID를 가지면 명령은 오류를
내고 성공 JSON을 출력하지 않습니다. 같은 내용이어도 서로 다른 저장소의 결과는
하나로 합치지 않습니다. 충돌은 `--repository`로 조회 범위를 좁혀 해결합니다.
보드 session을 얻지 못하거나 필요한 카드·등록 파일의 검증에 실패하면 부분 결과를
출력하지 않습니다. 등록 context 조회는 관계없는 카드 파일이나 ID 원장을 검사하지
않습니다.

저장소는 이름 순으로 조회합니다. 저장소들이 변경되지 않는 동안 같은 입력은 같은
결과를 냅니다. 서로 다른 저장소를 동시에 수정하는 동안의 원자적 snapshot은
보장하지 않으므로, 그 경우 작업을 멈춘 뒤 다시 조회해야 합니다.
검증과 조회 사이에 디렉터리를 교체하는 동시 변경도 감지한다고 약속하지 않습니다.

여러 카드 ID를 묶어 조회하면서 잠금 디렉터리조차 만들지 않아야 한다면 별도 계약인
[`workspace-context`](workspace-context.md)를 사용하세요. 이 명령은 기존의 정확히 하나인
`query-workspace` 결과 의미를 바꾸지 않습니다.
