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
- 현재 카드, claim 원장, transition 원장과 반복 가능한 `--id TASK-N` 값을 스캔한다.
  Git 이력은 읽지 않는다.
- ID는 `TASK-` 뒤에 양의 uint64 십진수가 오며 선행 0을 허용하지 않는다. 원장은
  단조롭게 커지고 최대 1MiB까지 유지되며 자동 정리하지 않는다.
- 예약은 같은 보드 잠금 아래 먼저 기록되므로 생성 실패 뒤에도 번호 공백이 생길 수 있다.
  생성 API는 idempotent하지 않으므로 출력 실패 뒤 `list`로 상태를 확인한다.
- 수동 작성 후 예약 명령이 관찰하기 전에 삭제한 카드, 원장 삭제·과거 revision 복원,
  독립 clone 간 동시 예약은 보호하지 못한다. 원장을 카드와 함께 백업·버전 관리한다.
