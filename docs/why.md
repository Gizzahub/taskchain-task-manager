# 왜 이 도구인가

사람과 코딩 에이전트가 **같은 Git 저장소 안의 작업 상태를 안전하게 공유**하도록
소유권, 중단 복구, 실행 이력을 제공하는 local-first task runtime입니다.
서버도, 계정도, 외부 에이전트 도구도 필요하지 않습니다.

이 문서는 설계 의도와 경계를 설명합니다. 명령별 계약은 [README](../README.md)와
`docs/`의 개별 문서가 정본입니다.

## 어떤 문제를 푸는가

에이전트에게 작업을 시키기 시작하면 태스크 상태가 세 곳으로 흩어집니다.
사람이 보는 이슈 트래커, 에이전트가 만든 마크다운 메모, 그리고 실제 코드입니다.
셋은 서로 다른 속도로 움직이고, 어느 것이 맞는지 판정할 근거가 없습니다.

마크다운 체크리스트로 통합하면 저장소 문제는 사라지지만 다른 문제가 남습니다.

- 두 에이전트가 같은 작업을 동시에 집어도 막을 수단이 없다.
- 프로세스가 중간에 죽으면 파일이 절반만 바뀐 상태로 남는다.
- 재시도가 안전한지, 이미 반영된 요청인지 구별할 수 없다.
- 사람이 손으로 파일을 옮긴 것과 도구가 옮긴 것을 구별할 수 없다.

이 도구는 파일 기반이라는 장점을 유지한 채 그 네 가지를 **런타임 계약으로**
해결합니다. 보드는 여전히 `git diff`로 읽히는 마크다운 파일이고, 동시성과
복구만 도구가 책임집니다.

## 60초 데모

아래 출력은 실제 실행 결과입니다.

```sh
$ ./build/taskchain-task-manager init --dir tasks --json
{"directory":"tasks"}

$ ./build/taskchain-task-manager create --dir tasks --title 'Write the spec' --json
{"path":"todo/TASK-1.md","card":{"id":"TASK-1","title":"Write the spec","status":"pending","priority":"","dependsOn":null}}

$ ./build/taskchain-task-manager create --dir tasks --title 'Implement the spec' --depends-on TASK-1 --json
{"path":"todo/TASK-2.md","card":{"id":"TASK-2","title":"Implement the spec","status":"pending","priority":"","dependsOn":["TASK-1"]}}
```

`ready`는 선행 작업이 끝나지 않은 TASK-2를 내보내지 않습니다. `ready`는 저장된
상태가 아니라 매번 계산되는 값입니다.

```sh
$ ./build/taskchain-task-manager ready --dir tasks --json
[{"path":"todo/TASK-1.md","card":{"id":"TASK-1","title":"Write the spec",...}}]
```

에이전트 A가 작업을 점유하면 `ready` 목록에서 빠집니다.

```sh
$ ./build/taskchain-task-manager claim --dir tasks --id TASK-1 \
    --owner agent-a --token aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa --json
{"id":"TASK-1","owner":"agent-a","token":"aaaa...","status":"held"}

$ ./build/taskchain-task-manager ready --dir tasks --json
[]
```

에이전트 B는 같은 카드를 점유할 수도, 상태를 옮길 수도 없습니다. 둘 다 종료
코드 1로 거부됩니다.

```sh
$ ... claim --id TASK-1 --owner agent-b --token bbbb... --json
claim: task TASK-1 is already claimed

$ ... transition --id TASK-1 --owner agent-b --token bbbb... --from todo --to doing --json
transition: matching held claim not found
```

점유자만 상태를 옮길 수 있고, 같은 `--request-id`로 재시도하면 두 번 적용되지
않고 같은 결과를 돌려줍니다. 재시도가 안전하다는 뜻입니다.

```sh
$ ... transition --id TASK-1 --owner agent-a --token aaaa... \
    --request-id 2222... --from todo --to doing --json
{"requestId":"2222...","id":"TASK-1","from":"todo","to":"doing","path":"doing/TASK-1.md","status":"completed"}

$ # 같은 요청을 그대로 다시 실행
{"requestId":"2222...","id":"TASK-1","from":"todo","to":"doing","path":"doing/TASK-1.md","status":"completed"}
```

결과는 평범한 파일입니다. 상태는 카드가 놓인 경로가 정본입니다.

```
tasks/.task-manager-ids.json
tasks/doing/TASK-1.md
tasks/todo/TASK-2.md
```

## 이 도구가 보장하는 것

**상태의 정본은 경로다.** 카드가 `doing/`에 있으면 상태는 `doing`입니다.
전이는 선언된 표를 따르며 임의의 점프를 허용하지 않습니다
([상태 전이](lifecycle.md)).

**소유권은 명시적이고 자동 만료되지 않는다.** `claim`은 owner와 token을 받고,
`transition`은 그 token을 요구합니다. 시간 기반 lease나 heartbeat는 없습니다.
서버가 없는 환경에는 신뢰할 수 있는 공유 시계가 없고, 만료는 죽은 프로세스와
느린 프로세스를 구별하지 못하기 때문입니다. 남은 claim은 사람이 해제합니다.

**재시도는 안전하다.** `transition`, `repair-status`, `relocate`, `archive`는
`--request-id`를 idempotency key로 받고, 기록된 요청을 그대로 다시 실행하면
중복 적용 대신 같은 결과를 반환합니다. `claim`은 `--resume`으로 재개합니다.

**중단은 복구된다.** 변경은 저널에 기록된 뒤 게시됩니다. 프로세스가 죽으면
`recover`가 기록된 미완료 전이를 재개하고, 미완료 전이가 남아 있는 동안
`ready`는 추측하는 대신 실패합니다.

**ID는 재사용되지 않는다.** 카드 파일을 지워도 ledger의 예약은 남습니다.
`TASK-2`를 삭제한 뒤 만든 카드는 `TASK-2`가 아니라 `TASK-3`입니다. `TASK-004`와
`TASK-4`는 같은 ID로 취급되어 충돌이 거부됩니다 ([ID 원장](ids.md)).
번호에 공백이 생기는 쪽이, 지워진 작업의 참조가 다른 작업을 가리키는 쪽보다
낫다고 판단했습니다.

**모든 것이 Git에 남는다.** 보드는 마크다운과 JSON 파일이라 `git log`가 그대로
작업 이력이 됩니다. 별도 감사 로그를 신뢰할 필요가 없습니다.

## 다른 접근과의 차이

특정 제품이 아니라 접근 방식의 축으로 비교합니다.

| | 서버형 트래커 | 일반적인 파일 기반 태스크 목록 | 이 도구 |
|---|---|---|---|
| 저장소 | 외부 DB | 저장소 내 파일 | 저장소 내 파일 |
| 계정·서버 | 필요 | 불필요 | 불필요 |
| 상태의 정본 | DB 레코드 | 관례(사람이 지킴) | 카드 경로(도구가 강제) |
| 동시 점유 방지 | 있음(서버 잠금) | 없음 | 있음(claim token) |
| 중단 복구 | 서버 트랜잭션 | 없음 | 저널 + `recover` |
| 재시도 안전성 | API에 따라 다름 | 없음 | request-id idempotency |
| 작업 이력 | 서버 감사 로그 | Git | Git |
| 코드와 함께 리뷰 | 불가 | 가능 | 가능 |

요약하면, 서버형 트래커의 안전성 중 **에이전트 협업에 실제로 필요한 부분만**
파일과 Git 위에서 재구성한 것입니다.

## 보장하지 않는 것

명시적인 비목표입니다. 필요하다면 다른 도구와 함께 쓰십시오.

- **작업이 실제로 끝났는지 판정하지 않습니다.** `validate-completion`은 체크박스를
  관측할 뿐이고, 카드가 `done/`에 있다는 것은 전이가 승인됐다는 뜻이지 구현이
  옳다는 뜻이 아닙니다.
- **테스트를 돌리거나 CI 결과를 읽지 않습니다.** PR·CI 연동은 선택적인 바깥
  계약이지 이 도구의 상태 모델이 아닙니다.
- **카드에 적힌 명령을 실행하지 않습니다.**
- **브랜치나 worktree를 만들지 않습니다.** Git 라이프사이클은 별개 관심사입니다.
- **에이전트를 실행하거나 스케줄링하지 않습니다.** 무엇을 먼저 할지 고르는 것은
  호출자의 몫이고, 이 도구는 고를 수 있는 것과 고를 수 없는 것만 알려줍니다.
- **분산 잠금이나 clone 간 자동 조정을 제공하지 않습니다.** 하나의 보드에는
  한 번에 하나의 writer를 전제합니다.
- **자동 만료, 강제 회수, 마감일, 알림이 없습니다.**
- **웹 UI와 릴리스 바이너리가 아직 없습니다.** 현재는 소스에서 빌드합니다.

## 현재 상태와 알려진 한계

초기 개발 단계입니다. 출력 계약은 안정화 전이며 변경될 수 있습니다.

알려진 한계 하나를 먼저 밝힙니다. 상태의 정본은 카드를 담은 **존**이고, 도구는
frontmatter의 `status` 필드를 재직렬화하지 않습니다. 따라서 전이 후 카드 본문의
`status: pending`은 경로와 어긋난 채로 남을 수 있습니다.

```
tasks/doing/TASK-1.md 의 frontmatter: status: pending   # 존이 정본, 이 값은 아님
```

`list --json`이 내보내는 `status`는 존이 결정합니다. frontmatter를 신뢰하지 마십시오.

다만 **모든 디렉터리가 상태를 표현하지는 않습니다.** 이 구분이 중요합니다.

| 존 | `status`의 출처 |
|---|---|
| workflow 존 (`todo` `doing` `review` `blocked` `done`과 그 별칭) | 존이 결정합니다. frontmatter는 무시됩니다 |
| kind 존 (`plan` `issue` `backlog`) | 존이 상태를 표현하지 않으므로 frontmatter가 유일한 출처입니다 |
| `archive` · `_archive` | 같습니다. frontmatter가 유일한 출처입니다 |
| 상태를 선언하지 않은 parked 존 | 같습니다. frontmatter가 유일한 출처입니다 |

**상태를 표현하지 않는 존 아래의 workflow 이름은 상태가 아닙니다.**
`archive/done/TASK-1.md`의 `done`은 이 카드가 *어디서* 보관됐는지를 뜻하고,
`plan/done/PLAN-1.md`의 `done`은 계획 안의 분류를 뜻합니다. 둘 다 `done` 상태라는
뜻이 아닙니다. 상태를 결정하는 것은 카드를 담은 존 하나뿐이며, 그 아래 디렉터리는
분류이거나 보관 이력입니다. 마찬가지로 아카이브는 의존성 완료를 부여하지 않습니다 —
그 판정은 별도의 완료 영수증을 거칩니다([아카이브](archive.md)).

frontmatter `status`는 카드 생성 시 한 번 기록되고, 그 뒤로는 `supersede`가 쓰는
`superseded` 외에 도구가 다시 쓰지 않습니다. 이 필드는 읽을 때 검증되지 않는 열린
문자열이므로 경로로 표현할 수 없는 임의의 값이 들어 있을 수 있습니다. 위 표에서
frontmatter가 유일한 출처인 존에서는 그 값을 그대로 돌려줍니다 — 도구가 지어내지 않습니다.

`show <file> --json`은 보드 정책 없이 파일 하나만 읽는 view입니다. workflow 존,
kind 존, 아카이브는 예약된 이름이라 정책 없이도 판정하지만, 선언된 모듈과 parked 존은
알지 못합니다. 또한 `show`는 경로에 `tasks/` 경계가 있을 때만 그 아래 디렉터리를
메타데이터로 읽습니다 — 경계가 없으면 파일 이름만 보고 frontmatter를 그대로 돌려줍니다.
보드 상태는 `list --dir <board> --json`으로 읽으십시오.

## 더 읽을 것

- [README](../README.md) — 설치, 전체 명령, 명령별 경계
- [상태 전이와 소유권](lifecycle.md)
- [ID 원장](ids.md)
- [카드 탐색](discovery.md)
- [상태 복구](status-repair.md)
- [완료 조건 관측](completion-observation.md)
