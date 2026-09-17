# 상태 전이와 복구

claim은 예약, transition은 카드 상태 이동, release는 예약 해제입니다.
transition은 claim을 자동 해제하지 않습니다. done은 구조적 상태이며 테스트 성공이나
업무 완료의 증명이 아닙니다. 필요한 검토·테스트·완료 증거 정책은 호출자가 적용해야 합니다.

## 명시적 요청

```sh
./build/taskchain-task-manager claim --dir ./tasks --id TASK-1 --owner developer --token 0123456789abcdef0123456789abcdef --json
./build/taskchain-task-manager transition --dir ./tasks --id TASK-1 --owner developer --token 0123456789abcdef0123456789abcdef --request-id 11111111111111111111111111111111 --from todo --to doing --json
```

token과 request-id는 예시입니다. 실제 새 예약·전이마다 새 32자리 lowercase hex 값을
각각 만들어 보관하세요. 결과가 불분명하면 **같은 값과 같은 인자**로 재시도합니다.
token은 예약 소유권, request-id는 전이 한 번의 재시도 식별자입니다. 인증 비밀은 아닙니다.
하나의 request-id를 다른 id/owner/token/from/to에 다시 사용할 수 없습니다.
완료된 동일 요청은 후속 전이·claim 해제 후에도 기존 receipt만 반환하며 재실행하지 않습니다.

| from | 허용 to |
|------|---------|
| todo | doing |
| doing | todo, review, blocked |
| blocked | todo, doing |
| review | doing, done |
| done | todo |

전이는 현재 workflow 경로와 from이 일치하고 정확한 held claim이 있어야 합니다.
top-level TASK와 [명시 채택한 module](module-adoption.md)의 workflow TASK를 지원합니다.
doing/done 진입 전에는 선행 TASK가 여전히 workflow done인지 재검사합니다.
동일 상태로의 새 전이, kind/archive 카드, legacy top-level 아래 임의 중첩 경로는 지원하지 않습니다.
release 후 doing/review/blocked/done에 남은 카드는 새 token의 `claim --resume`으로
명시적으로 예약할 수 있습니다. 기존 held claim을 강제로 회수하지는 않습니다.

## 원본과 저장

파일명과 기본 permission bits를 유지합니다. 경로가 상태의 정본이며, 본문의 첫 실제
Status 표 셀만 동기화합니다. frontmatter의 status·알 수 없는 필드·본문은 재직렬화하지 않습니다.
코드 fence 안의 예제, CRLF, 마지막 newline 유무, 상태 사유 tail을 보존합니다.
해당 표 셀이 없으면 내용 변경 없이 경로만 바뀝니다. inode·mtime·소유자·확장 속성 보존은
보장하지 않습니다. 원본과 패치 카드 각각 최대 1 MiB입니다.

보드 상태 조회에는 `list --dir <board> --json`을 사용하세요. `show <file> --json`은
단일 파일 view이며 이름이 tasks가 아닌 임의 보드의 discovery나 journal 검증을 하지 않습니다.
`validate <file> --json`도 구문 검사이지 board readiness/완료 판정은 아닙니다.

전이는 `.task-manager-transitions.json`에 pending을 먼저 쓰고 대상 파일을
no-overwrite 게시한 뒤 원본을 제거하고 완료 receipt를 기록합니다. 같은 board lock을 사용합니다.
Git 보드는 common namespace lock을 먼저 잡으며 completed receipt 재실행도 생략하지 않습니다.
공통 잠금 경합·손상·shared-ID 활성화 중에는 보드 작업이 오류로 중단됩니다. 이는 여러 worktree의
claim 소유권을 합치는 기능이 아닙니다. 자세한 범위는 [공유 ID 계약](shared-ids.md)을 따릅니다.
원장은 최대 8 MiB이고 재시도 기록을 자동 삭제하지 않습니다. 새 요청의 pending과 완료
기록 공간이 모두 부족하지 않아야 시작합니다. 원장을 지우면 재시도 안전성을 잃습니다.

## 중단 후 재개

pending이 남으면 일반 board 명령은 실패합니다. 동일 transition 요청을 다시 호출하거나
명령 이름만 `recover`로 바꾸고 나머지 인자를 동일하게 전달하세요. recover는 새 전이를 시작하지 않습니다.
단순 실패·출력 실패와 달리 프로세스 강제 종료는 `.task-manager.lock`도 남길 수 있습니다.
이때는 실제 실행 주체가 종료됐음을 확인해야 합니다. 이 도구는 lock을 자동 해제하지 않습니다.

재개는 현재 held 소유권과 journal을 검증하고 다음 상태만 허용합니다.

- 원본 일치·대상 없음: 대상 게시부터 진행.
- 원본 일치·패치 대상 일치: 원본 제거부터 진행.
- 원본 없음·패치 대상 일치: 완료 receipt 기록부터 진행.

다른 내용·권한·symlink·잘못된 경로·손상된 journal은 오류로 남기며 임의로 덮어쓰거나
삭제하지 않습니다. 이 경우 원본/대상/journal을 보존하고 먼저 원인을 확인하세요.
같은 보드에 두 engine을 동시에 쓰지 마세요. 전원 손실·네트워크 filesystem·분산 clone·
lock을 무시하는 외부 편집기까지의 원자성이나 내구성은 보장하지 않습니다.
