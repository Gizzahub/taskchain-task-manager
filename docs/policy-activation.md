# 불변 보드 정책 활성화

모든 보드 writer를 현재 버전으로 업그레이드하고 중지한 뒤 실행한다. 먼저
`validate-policy policy.yaml --json`으로 정책 문서의 의미를 확인한다.
활성화는 정책을 최초 채택하는 작업이며 기존 정책을 변경·삭제하는 명령이 아니다.
기존 정책 변경은 [명시적 policy revision](policy-revision.md)을 사용한다.

```sh
taskchain-task-manager activate-policy policy.yaml --dir ./tasks --json
# shared-ID namespace를 이미 사용하는 경우 최초 활성화는 모든 등록 worktree 대상이다.
taskchain-task-manager activate-policy policy.yaml --dir ./tasks --all-worktrees --json
```

shared-ID가 없는 보드는 local authority를 사용한다. shared-ID가 있으면 common authority를
사용하고 최초 활성화는 모든 등록 worktree의 같은 보드를 검증·채택한다. `--all-worktrees`
누락 시 최초 shared 활성화와 그 재개는 변경 없이 거부한다. 이 명령이 shared-ID를 새로
활성화하거나 worktree·Git branch를 생성하지 않는다.

새 worktree는 자동 채택되지 않는다. 그 보드에서 같은 정책으로 명령을 실행해 명시 join한다.
join과 이미 완료한 보드의 재호출에는 `--all-worktrees`가 필요 없다. 복제된 완료 파일도
새 보드의 활성화 증거가 아니다. 다른 canonical 정책은 거부하며 같은 정책의 local→shared
편입은 가능하다.

## 중단과 재개

진행 중 transition/bundle 또는 held claim이 있으면 최초 채택·join을 거부한다.
shared 채택 중에는 common pending 기록이 다른 보드의 작업도 차단한다.
중단된 기록이 있으면 원래 정책과 원래 보드에서 명시적으로 재개한다.

```sh
taskchain-task-manager activate-policy policy.yaml --dir ./tasks --resume --json
# 최초 shared 채택의 재개
taskchain-task-manager activate-policy policy.yaml --dir ./tasks --all-worktrees --resume --json
```

재개는 저장된 계획만 사용한다. 카드·ID·claim·bundle·HEAD/참여 worktree가 계획과
충돌하거나 필요한 파일이 유실되면 보존 후 실패한다. 파일·journal·lock을 삭제해
우회하거나 기본 정책으로 되돌리지 않는다. 프로세스 종료로 lock이 남으면 자동 탈취하지
않는다. 소유 프로세스의 종료와 관련 상태를 먼저 확인해야 한다.

완료 후 같은 정책 재호출은 결과 확인이며 정상적인 후속 카드·claim 변경을 과거 snapshot과
비교하지 않는다. 다른 pending 작업이나 손상된 authority가 있으면 결과 확인도 거부할 수 있다.
게시·정리·출력 오류가 나도 일부 또는 전체 변경이 완료됐을 수 있다. 상태를 보존하고
기록이 pending이면 `--resume`, 기록이 없으면 최초 명령, 완료됐으면 같은 정책으로 재확인한다.

protocol 5 archive가 있는 initial/join은 같은 hash-only archive binding을 common pending과
local receipt에 기록한다. empty archive namespace는 shared namespace로 한 번만 rebind할 수 있고,
이미 같은 namespace인 archive는 rewrite하지 않는다. receipt/payload는 capacity provenance일 뿐
policy activation 권한이 아니다.

## 호환성과 출력

local journal v3와 common state v3는 B024/B023 writer가 정책을 무시한 채 계속 쓰지
못하게 하는 저장 형식 장벽이다. 실제 바이너리의 list/ready/create/reserve-ids 거부와
local/common 무변경을 검증했다. 모든 과거 버전·외부 편집기까지 차단한다는 보장은 아니다.
특히 local pending 기록만 게시된 초기 구간은 구버전 writer가 모른다. 그 사이 파일이
변경되면 재개가 충돌로 거부한다. 따라서 혼합 버전 writer 운용은 지원하지 않는다.

성공 JSON은 `schemaVersion:1`, `authorityId`, `scope`(local/shared), `digest`,
`status:"completed"`, `replayed`, `boards`를 포함한다. `boards`는 이번 채택·재개에서
확인한 보드 수이며 완료 재호출은 1이다. 이는 테스트·리뷰·Intent 달성 증거가 아니다.
exit 0은 성공, 1은 입력·상태·게시·출력 오류, 2는 CLI 사용 오류다. 진단은 stderr에 쓴다.
