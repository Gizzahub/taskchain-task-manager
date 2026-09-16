# 영구 작업 ID 예약

`reserve-ids`는 보드에서 관찰한 작업 ID와 사용자가 지정한 과거 ID를
`.task-manager-ids.json`에 사전순으로 보존한다. 자동 할당은 숫자 최댓값 다음을 사용한다.

```sh
# 기존 보드의 현재 카드와 검토한 과거 ID를 합친다. 실제 이력에 맞는 ID를 지정한다.
./build/taskchain-task-manager reserve-ids --dir ./tasks --adopt --id TASK-12 --id TASK-19 --json
```

반복 실행은 합집합만 기록한다. 기존 원장을 초기화하지 않으며 카드 내용은 변경하지 않는다.
채택 후 필요한 기본 디렉터리가 없으면 `init --dir ./tasks --json`으로 준비할 수 있다.

- 새로 만든 루트의 `init`만 빈 ID 원장을 자동 생성한다. 기존 보드에 원장이 없으면
  `reserve-ids --adopt --json`을 명시적으로 실행해야 한다.
- 현재 카드, claim 원장, transition 원장과 반복 가능한 `--id PREFIX-N` 값을 스캔한다.
  Git 이력은 읽지 않는다.
- ID는 `TASK`, `PLAN`, `ISSUE`, `BACKLOG` 뒤에 uint64 십진수가 온다. 입력과 카드에서는
  0과 선행 0을 읽으며 `TASK-090`과 `TASK-90`은 같은 identity다. 원장의 key는 패딩 없이
  저장한다. 원장은 단조롭게 커지고 최대 1MiB까지 유지되며 자동 정리하지 않는다.
- 예약은 같은 보드 잠금 아래 먼저 기록되므로 생성 실패 뒤에도 번호 공백이 생길 수 있다.
  생성 API는 idempotent하지 않으므로 출력 실패 뒤 `list`로 상태를 확인한다.
- 수동 작성 후 예약 명령이 관찰하기 전에 삭제한 카드, 원장 삭제·과거 revision 복원,
  독립 clone 간 동시 예약은 보호하지 못한다. 원장을 카드와 함께 백업·버전 관리한다.

## 종류와 호환성

`create --kind plan`은 `plan/PLAN-N.md`를 만든다. issue/backlog도 각각의 디렉터리에
생성하고 번호는 종류별로 독립적이다. 기본값은 기존과 같은 `todo/TASK-N.md`다.
명시한 `--id ISSUE-007`은 해당 표기를 보존한다. `--kind`도 주면 prefix와 일치해야 한다.
자동 생성은 1부터 시작하고 패딩을 추가하지 않는다. CE 원본의 최소 3자리 표기를 자동으로
덮어쓰지 않으며 CE signed int보다 큰 제품 uint64 ID의 CE 사용 가능성을 보장하지 않는다.

숫자 identity로 카드 중복, 의존성 중복·자기참조·순환, held claim 충돌을 검사한다.
원본 카드와 경로는 변경하지 않는다. 실행 대상은 TASK prefix의 top-level workflow 카드뿐이다.
PLAN/ISSUE/BACKLOG를 todo/done에 놓아도 ready나 완료된 선행 작업으로 인정하지 않는다.
종류 카드도 참조는 저장할 수 있지만 TASK 완료 의존성을 충족시키지는 않는다.

신규 원장은 schemaVersion 2다. 기존 v1 TASK-only 원장은 읽고, 성공하는 create/reserve-ids에서
원자적으로 v2로 갱신한다. 구버전 binary는 v2를 거부한다. v1의 기존 검증 규칙은 완화하지 않는다.
예약 결과의 `maxId`는 기존처럼 TASK 최댓값이며 없으면 빈 문자열이다. 추가 필드 `maxIds`는
존재하는 prefix별 최댓값을 제공하며 0도 포함한다. 빈 prefix의 첫 자동 번호는 1이다.

claim/token 또는 transition/request-id 재시도에는 처음 요청한 ID 문자열을 그대로 쓴다.
release에는 claim receipt의 ID를 쓴다. 숫자가 같아도 요청 문자열을 바꿔 재시도하지 않는다.
충돌 검사는 alias를 같은 작업으로 취급하므로 다른 token으로도 이중 예약할 수 없다.
로컬 Git ref에서 도달 가능한 삭제 이력은 명시적인 [Git ID 가져오기](git-import.md)로
예약할 수 있다. 공유 worktree 예약·CE 전체 dialect 호환은 아직 지원하지 않는다.
