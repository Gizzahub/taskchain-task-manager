# 명시적 kind 경로 이동

`relocate`는 명시된 kind edge로 카드 파일을 이동합니다. ID·frontmatter kind/type은
변경하지 않으며, PLAN을 todo로 옮겨도 실행 가능한 TASK로 바뀌지 않습니다.
일반 workflow 전이, archive, supersede, 완료 증거 판정과는 별도 작업입니다.

## 정책과 범위

새 보드에서 다음과 같은 v2 정책을 `activate-policy`로 명시 채택할 수 있습니다.
이미 활성화된 다른 정책은 자동 덮어쓰지 않습니다. 기존 v1 정책의 revision 전환과
명시적 module discovery는 아직 지원하지 않습니다. 보드 바로 아래 kind/workflow 경로와
그 아래 category 경로를 사용하세요. module 이름을 kind 경로로 바꾸어 우회하지 마세요.

```yaml
schema-version: 2
board-policy:
  relocations:
    - from: todo
      to: [plan]
    - from: plan
      to: [todo]
  kind-status:
    plan: pending
```

각 edge의 적어도 한쪽은 plan/issue/backlog입니다. source는 workflow/kind/선언 parking,
target은 workflow/kind입니다. parking target, archive, same-zone은 거부합니다.
module/category로 해석될 위치에 workflow alias나 추가 zone 이름이 있으면 거부합니다.
category와 파일명은 보존해야 합니다. target workflow의 정규 상태 또는 명시 kind-status로
첫 본문 Status cell만 보정합니다. 매핑이나 cell이 없으면 원본 bytes를 보존합니다.

## 실행과 저장 형식

모든 writer를 중지·업그레이드하고 보드와 공유 상태를 함께 백업한 뒤 최초 요청에
`--adopt`를 사용합니다. storage v2 채택 후 구버전은 접근을 거부합니다. marker/journal을
삭제해서 downgrade하지 마세요. 기존 repair receipt는 별도 journal에 그대로 남습니다.

```sh
shasum -a 256 ./tasks/todo/TASK-1.md
./build/taskchain-task-manager relocate \
  --dir ./tasks --id TASK-1 \
  --source todo/TASK-1.md --target plan/TASK-1.md --owner developer \
  --request-id 11111111111111111111111111111111 \
  --expected-sha256 '<확인한 원본 SHA-256>' --adopt --json
```

카드 ID는 이미 영구 예약되어 있어야 합니다. held claim이 있으면 정확한 owner/token이
필요하며, 없으면 token을 생략합니다. TASK의 doing/done 진입에는 기존 held claim과
완료 의존성이 필요합니다. 명령은 claim을 만들거나 자동 해제하지 않습니다.
최초 target이 존재하면 내용이 같아도 충돌입니다. 기존 파일을 덮어쓰지 않습니다.

성공 JSON은 schemaVersion/requestId/id/source/target/status/changed를 포함합니다.
status=completed는 이동 요청의 완료일 뿐입니다. changed는 **본문 bytes 보정 여부**이며,
false여도 경로는 이동합니다. 출력 실패 후에도 이동은 이미 완료됐을 수 있습니다.

## 중단 복구

원래 source/target/hash/owner/token/request ID로 재시도합니다. `--resume`은 기록된
요청만 복구하며 새 요청이나 최초 채택을 시작하지 않습니다. 채택 도중 아직 요청이
기록되지 않았다면 동일 요청과 `--adopt`로 재시도하세요. 두 옵션을 함께 쓰지 않습니다.

pending 동안 일반 board 명령과 공유 namespace의 다른 writer는 거부됩니다.
정확한 target이 게시된 중간 상태만 복구 대상으로 인정합니다. 원본·대상 bytes/mode,
정책·identity·journal 충돌은 보존하고 중단합니다. 완료 receipt 재생은 다시 이동하지 않습니다.
실제 프로세스 종료 확인 없이 잠금을 지우지 마세요. 자동 잠금 회수는 없습니다.

기본 permission bits는 보존하지만 inode/mtime/소유자/확장 속성 보존을 보장하지 않습니다.
Git commit/worktree를 만들지 않으며 다른 clone을 조정하지 않습니다. 비협조 외부 편집기,
전원 손실 또는 네트워크 filesystem까지의 원자성을 보장하지 않습니다.
