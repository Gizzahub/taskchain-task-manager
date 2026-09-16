# Worktree 읽기 전용 진단

```sh
./build/taskchain-task-manager inspect-worktrees --repo ./source-repo --board tasks --json
```

현재 저장소의 Git common directory와 등록된 worktree 목록을 관측한다. 보드의 상대 경로와
그 UTF-8 byte에 대한 SHA-256 `namespaceKey`도 반환한다. 같은 Git common directory와 같은
보드 경로가 한 namespace를 식별한다. key만으로 다른 저장소까지 같은 보드라고 판단하지 않는다.

출력에는 schemaVersion, repository, commonDirectory, board, namespaceKey, worktrees,
sharedReadiness가 있다. worktree별 path/head/branch 또는 detached/bare 상태와
locked/prunable 여부·사유를 보존한다. 경로순으로 정렬하며 최대 256개다.

`sharedReadiness`는 항상 `not_evaluated`다. 다른 worktree의 현재 카드·원장·접근 가능 여부나
공유 예약 정책은 아직 검사하지 않는다. 목록의 locked는 Git worktree 관리 잠금이지 태스크
writer의 잠금이 아니다. prunable 항목을 자동 제거하거나 정상 보드로 간주하지 않는다.

`--repo`는 실제 worktree 루트, `--board`는 그 안에 존재하는 정확한 상대 디렉터리 경로다.
symlink·대소문자 별칭·중첩 Git repository 경계·Git metadata 내부·누락 보드는 오류다.
일반 Git 이력 검사의 shallow/replace/graft/partial/env override 제한과 bounded runner를
사용한다. 지원하지 않는 Git 출력 필드·손상된 목록은 일부만 반환하지 않고 실패한다.

관측 전후 worktree 목록·common directory 경로/identity와 현재 board identity가 바뀌면 오류다.
이 검사는 전역 잠금이 아니며 종료 후 변경이나 중간 변경 후 원상복구까지 보장하지 않는다.
namespace 생성, 예약 활성화, Git fetch/prune/repair, 카드 변경은 하지 않는다.
이 출력은 활성화 승인이나 이관 완료 증거가 아니다. 실제 채택은 [공유 ID 사용법](shared-ids.md)을 따른다.
