# 같은 Git 저장소의 worktree 간 ID 예약

공유 모드는 **ID 할당만** 조정한다. claim·transition의 분산 실행권이나 서로 다른 clone 간
잠금이 아니다. 기존 모든 writer를 중단하고 보드 및 Git common directory를 백업한 뒤 채택한다.

```sh
./build/taskchain-task-manager inspect-worktrees --repo ./repo --board tasks --json
# 모든 등록 worktree의 동일 상대 경로 보드를 변환한다.
./build/taskchain-task-manager enable-shared --dir ./repo/tasks --all-worktrees --json
# 중단된 활성화를 조사한 뒤, 원래 inventory/카드가 유지된 경우에만 재개한다.
./build/taskchain-task-manager enable-shared --dir ./repo/tasks --all-worktrees --resume --json
```

각 worktree에는 기존 local ID 원장과 정상 보드가 있어야 한다. bare·locked·prunable·접근 불가
worktree, 누락 보드, pending transition, 잘못된 카드·원장은 오류다. 자동 생성·수리·prune하지 않는다.
원장이 없는 보드는 먼저 `reserve-ids --adopt`로 채택한다. Git 이력과 각 worktree의 미커밋
카드·예약·claim/transition 이력을 합친다. 카드 원문이나 ID 패딩은 변경하지 않는다.

## 저장과 쓰기 순서

- 공통 원장은 `<git-common-dir>/taskchain-task-manager/ids/<board-path-sha256>/state.json`이다.
  저장소 상대 보드 경로, namespace 식별자, phase, 정규화된 예약 집합, 활성화 snapshot을 기록한다.
- local 원장은 schemaVersion 3과 namespace binding으로 변환한다. 구버전 제품은 이를 거부한다.
  공통 원장과 local 원장을 함께 보존한다. 공통 상태 삭제를 비활성화 방법으로 사용하지 않는다.
- 모든 Git 보드의 `init/create/reserve-ids/import-ids`는 공통 namespace 잠금을 먼저 잡는다.
  공유 활성화 전에도 이 검사를 위한 Git metadata 디렉터리가 만들어질 수 있다.
  일반 standalone 디렉터리는 기존 local 예약을 사용한다.
  Git 보드는 local 모드에서도 Git topology 검증을 수행하므로 shallow/partial/replace/graft나
  Git 경로·설정 override가 있으면 오류다. 이를 standalone으로 조용히 취급하지 않는다.
- 공유가 active이면 일반 명령도 자동으로 이를 따른다. 별도 `--shared` 옵션은 없다.
  새 worktree의 local v2 원장도 새 writer가 공통 원장에 연결한다. 원장 자체가 없으면 명시 채택한다.
- 자동 번호는 local 관측·Git 이력·공통 예약 최댓값 다음이다. 명시 ID는 예약되지 않은 hole을
  허용하며 `TASK-090`과 `TASK-90`의 동시 예약은 같은 identity 충돌이다.
- 공통 선예약 → local 원장 → 카드 게시 순서다. 뒤 단계가 실패하면 번호가 소비될 수 있다.
  생성은 멱등 명령이 아니므로 오류에 나온 ID와 실제 카드를 확인한 후 재시도한다.

## 중단과 복구

활성화는 initializing → 모든 local v3 게시 → inventory/snapshot 재검증 → active 순서다.
initializing 동안 일반 ID writer는 오류로 중단한다. 원래 기록과 현재 상태가 맞을 때만
`--resume`이 전진한다. 예약 집합을 줄이거나 원장을 초기화하는 복구는 하지 않는다.
worktree 추가·이동·누락 또는 카드/원장 변경이 있으면 임의로 수리하지 말고 원인부터 확인한다.

프로세스를 강제 종료하면 namespace 또는 board의 `.task-manager.lock` 디렉터리가 남을 수 있다.
자동 해제하지 않는다. 관련 writer가 모두 종료된 것이 확인되기 전에는 잠금을 제거하지 않는다.
중단 테스트의 정상 오류 반환과 달리, 강제 종료 후에는 잠금 조사와 명시 복구가 필요하다.

같은 namespace의 모든 writer를 새 버전으로 통일해야 한다. marker 없는 새 worktree에서
구버전이 직접 init하거나 외부 편집기가 쓰는 것을 막는 인증 시스템은 아니다.
실행 중 Git/worktree/보드를 외부에서 바꾸지 않는다. 전원 손실·네트워크 filesystem·외부
writer·공통 원장의 과거 revision 복원까지 자동 복구하는 기능은 아니다. CE 원장과 호환 쓰기를
하지 않으며 CE와 동시에 같은 운영 보드를 쓰지 않는다.
