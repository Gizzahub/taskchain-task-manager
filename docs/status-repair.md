# 같은 경로의 Status cell 보정

`repair-status`는 현재 workflow 경로를 정본으로 삼아 본문의 첫 Status cell만 보정합니다.
파일을 이동하거나 workflow 전이를 허용하지 않습니다. frontmatter status, ID, 다른 필드와
본문을 재직렬화하지 않습니다. 완료 증거나 실제 구현의 성공을 판정하지 않습니다.

## 요청 준비

현재 보드 바로 아래의 `todo/doing/review/blocked/done`에 있는 TASK 카드만 지원합니다.
kind 카드, archive, 중첩 모듈과 parking 경로는 이 명령의 대상이 아닙니다.
이미 올바르거나 Status cell이 없는 카드는 bytes를 바꾸지 않고 `changed: false`를 반환합니다.

대상 원본 파일의 SHA-256을 구해 `--expected-sha256`에 전달합니다. 경로는 board-relative
정확한 경로이며 hash는 원본 전체 bytes의 lowercase hex 64자리입니다. 새 요청마다 새
32자리 lowercase hex request ID를 만들고 모든 인자를 보관하세요. 다음 값은 예시입니다.

```sh
# 먼저 원본을 읽어 검토하고 hash를 기록합니다.
shasum -a 256 ./tasks/todo/TASK-1.md

./build/taskchain-task-manager repair-status \
  --dir ./tasks --id TASK-1 --path todo/TASK-1.md --owner developer \
  --request-id 11111111111111111111111111111111 \
  --expected-sha256 '<확인한 원본 SHA-256>' --adopt --json
```

최초 `--adopt` 전에 이 보드와 같은 저장소를 쓰는 모든 writer를 업그레이드하고 중지하세요.
채택은 저장 형식의 변경입니다. 구버전은 새 상태를 무시하지 않고 거부해야 합니다.
채택 후 새 repair 요청에는 `--adopt`가 필요하지 않습니다. marker·journal을 삭제해
구버전으로 되돌리지 마세요. rollback에는 일관된 전체 보드·공유 상태 백업이 필요합니다.

held claim이 있으면 같은 owner와 정확한 `--token`을 전달해야 합니다. 없으면 token을
생략합니다. owner는 로컬 작업 식별자이지 인증 수단이 아닙니다. repair가 claim을 새로
만들거나 자동 해제하지 않습니다. 완료 후 기존 `release`를 사용할 수 있습니다.

## 결과와 재시도

성공은 exit 0과 JSON 한 개이며 `schemaVersion`, `requestId`, `id`, `path`,
`status: "completed"`, `changed`를 포함합니다. `completed`는 이 보정 요청의 완료일 뿐입니다.
stdout 쓰기가 실패해도 작업은 이미 완료됐을 수 있습니다.

원래 인자와 **원래 hash·request ID**로 재시도하세요. 현재 파일의 hash로 바꾸면 동일 요청이
아닙니다. `--resume`은 기록된 요청만 재개하며 새 요청이나 형식 채택을 시작하지 않습니다.
`--adopt`와 `--resume`은 함께 사용할 수 없습니다. 완료된 요청은 과거 결과만 반환합니다.

pending 동안 일반 board 명령은 거부됩니다. 원본 bytes이면 보정을 계속하고, 정확한 목표
bytes이면 완료 기록을 마무리합니다. 다른 bytes·mode·symlink·파일 부재는 덮어쓰지 않습니다.
잠금이 남았다는 이유만으로 제거하지 마세요. 실제 프로세스 종료를 확인한 뒤 journal과
카드를 보존하고 복구해야 합니다. 자동 잠금 만료·강제 회수는 없습니다.

기본 permission bits는 보존하지만 inode·mtime·소유자·확장 속성 보존을 보장하지 않습니다.
같은 filesystem에서 staged write와 atomic replacement를 사용합니다. 전원 손실,
네트워크 filesystem이나 잠금을 무시하는 외부 편집기와의 원자성까지 보장하지 않습니다.
실행 중에는 외부에서 카드를 편집하지 마세요. 다른 clone 사이의 분산 실행 조정도 아닙니다.
