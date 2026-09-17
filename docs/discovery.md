# 현재 보드의 카드 발견 규칙

`list`, `ready`, `create`, 예약·소유권·전이 명령은 같은 현재 보드 발견 규칙을 사용한다.
보드 최상위 workflow는 todo/doing/review/blocked/done이며 종류 영역은 plan/issue/backlog다.
`archive`와 `_archive`는 모두 보관 영역이다. 이름을 자동 변경하거나 카드를 이동하지 않는다.
양쪽 보관 영역의 카드도 목록·중복 identity 검사·ID 예약에 포함한다.
[명시적으로 채택한 module](module-adoption.md)에서도 같은 zone을 발견하며,
`module/zone/category/card.md` 경로의 module과 category를 유지한다.

알려진 영역 아래 `.md` 파일을 재귀적으로 읽는다. 다음은 카드가 아니다.

- README.md, INDEX.md, TEMPLATE.md: 전체 파일명 기준이며 대소문자는 구분하지 않는다.
- 보드 아래 `.ce`와 `evidence` 디렉터리: 정확히 이 소문자 이름만 제외한다.
- 현재 보드 조회에서는 점으로 시작하는 파일과 중첩 디렉터리도 제외한다.

이름이 비슷한 `INDEX-extra.md`, `evidence.md`, `Evidence/`는 제외하지 않는다.
선택한 보드의 상위 디렉터리 이름은 제외 규칙과 무관하다. 제외된 문서나 evidence 안의
`id:` 예시는 현재 카드나 예약 대상이 아니다. 실제 카드를 그 위치에 보관하지 않는다.

보드 루트의 일반 카드·알려지지 않은 디렉터리는 오류다. workflow 별칭이나 임의 layout을
자동 변환하지 않는다. 발견한 symlink는 제외 이름이라도 거부하며 따라가지 않는다.
제외된 실제 디렉터리 내부는 검사하지 않는다. malformed YAML·중복 identity는 오류다.

보관 카드의 상태가 done이어도 선행 작업 완료로 인정하지 않는다. 실행 가능한 workflow는
top-level 또는 선언된 module의 workflow TASK다. kind/archive와 legacy top-level 아래의
임의 중첩 카드는 claim이나 transition 대상이 아니다. module의 category는 지원한다.

Git 이력 스캔은 다른 목적으로 더 보수적으로 읽는다. 명시 비카드 이름은 같지만 `.ce` 이외의
숨김 디렉터리·숨김 Markdown도 과거 ID 후보로 읽을 수 있다. 전체 CE configurable dialect,
완료 증거 판정, 공유 worktree 예약을 지원한다는 뜻은 아니다.

## 이전 버전에서 업그레이드

이전 버전은 INDEX.md/TEMPLATE.md 및 알려진 영역 아래 evidence 안의 파일을 카드로
읽을 수 있었다. 업그레이드 전에 실제 카드가 이 위치에 없는지 확인하고 원본과 원장을
백업한다. 이전 바이너리로 pending 전이를 먼저 복구한 뒤 ID를 예약한다. 소유권이 남아
있으면 해제하고 일반 카드 경로로 이동한 뒤 필요한 소유권을 다시 획득한다.

제외 이름의 pending journal을 새 버전이 발견하면 이전 바이너리로 복구하라는 오류로
중단한다. journal이나 claim 원장을 지워서 우회하지 않는다. 새 제외 규칙은 실제 카드의
자동 이관이 아니며, Git import 역시 제외된 과거 파일의 ID를 대신 예약하지 않는다.
