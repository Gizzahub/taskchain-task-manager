# TaskChain Task Manager

개발자와 코딩 에이전트를 위한 파일 기반 태스크 관리 도구입니다.
현재는 초기 개발 단계이며 카드 codec과 **초기화·생성·목록·조회·의존성 기반 ready·구문 검증**을 제공합니다.
claim/release와 소유권 기반 상태 전이·재개를 지원합니다. Intent/Batch/Iteration 문서의 구문 검증과 불변 등록·조회를 제공합니다.

## 빌드와 실행

Go 1.26.6 이상이 필요합니다. 서버, 계정, 외부 에이전트 도구는 필요하지 않습니다.

```sh
make check
./build/taskchain-task-manager show examples/card.md --json
./build/taskchain-task-manager validate examples/card.md --json
./build/taskchain-task-manager init --dir ./tasks --json
./build/taskchain-task-manager create --dir ./tasks --title '첫 작업' --json
./build/taskchain-task-manager list --dir ./tasks --json
./build/taskchain-task-manager create --dir ./tasks --title '후속 작업' --depends-on TASK-1 --json
./build/taskchain-task-manager ready --dir ./tasks --json
./build/taskchain-task-manager validate-context examples/context/intent.json --json
./build/taskchain-task-manager register-context examples/context/intent.json --dir ./tasks --json
./build/taskchain-task-manager show-context --dir ./tasks --kind intent --id INTENT-0123456789abcdef0123456789abcdef --revision 1 --json
```

`validate`는 YAML frontmatter의 구문·형식을 검사합니다. 완료 조건, 의존성,
실제 구현의 정확성이나 작업 완료 여부를 판정하는 명령은 아닙니다.
카드 내용의 명령을 실행하지 않습니다. `show`·`validate`는 읽기 전용이고,
`init`·`create`는 지정한 보드에 새 디렉터리·카드를 만듭니다.
`transition`은 소유권을 확인해 카드를 이동하고 `recover`는 기록된 미완료 전이를 재개합니다.

`validate-context`는 하나의 strict JSON Intent/Batch/Iteration 문서를 읽어 canonical bytes와
SHA-256 digest를 출력합니다. 보드 등록, TASK 존재 확인, 권한 부여, 실제 목표 달성
판정은 하지 않으며 입력 파일을 수정하지 않습니다. 문서 한도와 필드는
[Intent/Batch 문서 검증](docs/context-validation.md)을 따릅니다.

`register-context`는 검증된 문서를 보드의 불변 context registry에 등록하고,
`show-context`는 명시한 kind·ID·revision 하나를 조회합니다. 등록은 board-local이며
자동 latest/head, Git 이력, common worktree 공유, task 생성, 평가 실행을 제공하지
않습니다. 같은 canonical bytes 재시도는 unchanged이고 다른 bytes는 충돌입니다.
자세한 참조 검증과 재시도 경계는 [Intent/Batch context registry](docs/context-registry.md)를
참조하세요.

유지형 Intent는 trigger와 유한 예산을 선언하고, Iteration은 기준별 관측·사용량·중단
사유를 기록합니다. 작업 없는 idle도 기록할 수 있습니다. 스케줄러나 실행기는 아니며
실제 예산 집행·증거 인증은 하지 않습니다. [유지형 관측 기록](docs/maintenance-iterations.md)을
참조하세요.

`create-bundle`은 정확한 등록 Intent에 연결된 여러 TASK와 Batch를 하나의 복구 가능한
요청으로 게시합니다. 명시적 프로토콜 채택·동일 요청 재개와 안전 경계는
[Task 일괄 생성](docs/task-bundles.md)을 따릅니다. LLM 계획 생성이나 실행 loop는 아닙니다.

`activate-policy`는 검증된 정책을 local/shared 보드에 불변으로 채택합니다.
모든 writer 업그레이드·중지, 최초 shared 채택의 `--all-worktrees` 확인,
중단 재개·새 worktree join 절차는 [정책 활성화](docs/policy-activation.md)를 따릅니다.

## 생성·목록의 안전 경계

- 기본 `create`는 `todo/TASK-N.md`에 새 카드를 만듭니다. 기존 파일을 덮어쓰지 않습니다.
- `--id`를 생략하면 해당 종류의 현재 카드·소유권/전이 이력·영구 ID 원장 최댓값 다음을 할당합니다.
  새로 생성하거나 명시적으로 예약한 ID는 카드 삭제 후에도 다시 쓰지 않습니다.
  기본 local 모드는 Git 이력을 자동 조회하지 않습니다. 공유 ID 모드는 생성 때 이력을 확인합니다.
  기존 보드와 과거 ID는 [ID 예약·채택](docs/ids.md)을 따릅니다.
- `list`는 알려진 workflow·종류·archive/_archive 디렉터리를 검사하고 경로순 결과를 반환합니다.
  ID 중복·잘못된 카드·symlink를 발견하면 조용히 건너뛰지 않고 실패합니다.
- TASK/PLAN/ISSUE/BACKLOG ID를 읽고 `create --kind`로 종류별 카드를 생성합니다.
  숫자 ID의 패딩은 표기 차이이며 원본은 보존합니다. 실행 lifecycle은 TASK만 지원합니다.
  새 카드 파일명은 `.md`를 포함해 255 bytes 이하여야 하며, 초과하면 ID 예약 전에 거부합니다.
  기존 도구의 모든 dialect·Git 예약 방식과 호환된다는 뜻은 아닙니다.
- board 명령은 `.task-manager.lock`을 사용합니다. 다른 프로세스의 잠금이 있으면
  즉시 실패하며 자동으로 강제 해제하지 않습니다. 중단 뒤 잠금이 남으면 실행 중인 작업이
  없는지 확인하고 복구를 판단해야 합니다. 잠금이 있다는 이유만으로 삭제하지 마세요.
- 생성은 같은 filesystem의 임시 파일을 완성하고 ID를 영구 예약한 뒤 hard link로 공개합니다.
  예약 이후 실패하면 번호가 소비될 수 있으며 자동 재사용하지 않습니다.
  hard link를 지원하지 않는 filesystem은 오류를 반환합니다. 전원 손실·네트워크 filesystem·
  잠금을 무시하는 외부 편집기까지의 일관성을 보장하지 않습니다.
- 생성 후 출력이나 정리 단계가 실패하면 카드가 이미 존재할 수 있습니다.
  재시도 전에 `list`와 대상 파일을 확인하세요. 실행 중에는 보드를 외부에서 편집하지 마세요.

## 의존성과 ready

`create --depends-on TASK-1 --depends-on TASK-2`처럼 선행 작업을 각각 지정합니다.
파일에는 `depends-on: [TASK-1, TASK-2]`로 저장됩니다. 기존 단일 문자열도 읽지만
숫자·객체·빈 문자열 항목은 오류입니다. 필드 없음/null/빈 목록은 의존성 없음입니다.

`ready`는 바로 아래 `todo/`의 pending 카드 중 모든 선행 카드가 바로 아래 `done/`에
있는 항목을 경로순으로 반환합니다. 없으면 `[]`입니다. 우선순위 정렬은 아직 없습니다.
kind 디렉터리(plan/issue 등)·archive·중첩 카드의 상태 표기만으로 완료를 인정하지 않습니다.
`ready`와 `create`는 전체 그래프의 누락 참조·중복 의존성·자기참조·순환을 거부합니다.
별도의 순환이 있어도 일부 정상 후보만 반환하지 않습니다. `list`는 관계 오류를 진단하기
위해 읽을 수 있지만 카드 자체나 소유 원장이 잘못된 형식이면 실패합니다.
기존 도구의 전체 dialect·완료 증거 계약을 이 기능만으로 대체하지 마세요.

## 실행권 예약

```sh
# 실제 새 시도에는 새 32자리 lowercase hex token을 만들어 보관하세요.
./build/taskchain-task-manager claim --dir ./tasks --id TASK-1 --owner developer --token 0123456789abcdef0123456789abcdef --json
./build/taskchain-task-manager release --dir ./tasks --id TASK-1 --owner developer --token 0123456789abcdef0123456789abcdef --json
```

claim은 ready 작업을 예약하며 카드의 내용·경로·상태를 변경하지 않습니다. held인 작업은
ready에서 제외됩니다. release는 예약만 해제하며 작업 완료를 의미하지 않습니다.
owner는 명시적 로컬 식별자이고 token은 재시도 식별자이지 인증 수단이 아닙니다.
출력 실패 등으로 결과가 불분명하면 같은 id/owner/token으로 재시도하세요.
다른 소유자의 해제, token의 다른 작업 재사용, released token 재획득은 거부합니다.
동일 held claim 또는 동일 release 재시도는 같은 결과입니다. 자동 만료·강제 회수는 없습니다.
`claim --resume`은 명시적으로 선택한 unclaimed doing/review/blocked/done 카드를 예약합니다.
다른 held claim을 빼앗거나 kind/archive/nested 카드를 예약하지 않습니다.

보드의 `.task-manager-claims.json`에 예약과 해제 이력을 보존합니다. 삭제·수동 편집하면
중복 실행 방지와 재시도 기록을 잃습니다. 다른 clone·worktree 사이의 분산 잠금은 아닙니다.
원장은 1 MiB로 제한하며 신규 claim에는 향후 release 공간도 예약합니다. 자동 이력 삭제는
없습니다. 손상·미지원 버전·중복 키·symlink는 오류이며 list/create/ready도 이를 무시하지 않습니다.
staged write 뒤 rename으로 교체하며 전원 손실이나 잠금을 무시하는 편집기는 보장 범위 밖입니다.

## 출력 계약 (초기, 안정화 전)

- 성공: exit 0, stdout에 JSON 한 개. config 없는 `validate`는 `{"valid":true}`인 구문 검사입니다.
- `validate FILE --config RULES --json`은 명시적 단일 카드 규칙과 criteria를 검사합니다.
  규칙 위반은 exit 1과 JSON findings를 반환하며 전체 보드나 완료 증거의 검증이 아닙니다.
- 파일/구문/출력 오류: exit 1, stderr에 원인, 입력 오류 시 stdout은 비어 있음.
- 잘못된 명령/인자: exit 2. `--help`는 exit 0.
- `show`는 알려진 메타데이터의 view이며 전체 원본 문서가 아닙니다.
- `tasks/` 아래 workflow 경로의 상태가 frontmatter보다 우선합니다. 다른 경로에서는
  frontmatter 상태를 사용합니다. 원본 파일은 수정하지 않습니다.
- 잘못되거나 끝나지 않은 YAML은 오류입니다. 이를 조용히 무시하지 않습니다.

내부 codec은 원본 byte를 보존하고 본문의 첫 Status 셀만 변경할 수 있습니다.
상태 patch는 소유권을 확인하는 `transition`에서 사용하며 안정적인 외부 Go API는 아닙니다.
알 수 없는 필드는 파생 view에서 생략되지만 원본 byte에는 보존됩니다.
명시적인 [Git 이력 ID 가져오기](docs/git-import.md)를 지원합니다.
Git branch/worktree 관리·범용 카드 편집·분산 잠금은 아직 지원하지 않습니다.

상태 전이, 멱등 요청, pending journal 복구와 지원 한계는 [상태 전이 사용법](docs/lifecycle.md)을 읽어 주세요.
보관 영역과 비카드 문서의 구분은 [카드 발견 규칙](docs/discovery.md)을 따릅니다.
[worktree 읽기 전용 진단](docs/worktree-inspection.md)으로 활성화 전 topology를 확인합니다.
같은 저장소의 worktree 간 ID 할당은 [공유 ID 채택·복구](docs/shared-ids.md)를 따릅니다.
설정 필드·검증 범위·입력 제한은 [단일 카드 규격 검증](docs/card-validation.md)을 따릅니다.
같은 규칙을 생성에도 적용하려면 [명시 규칙으로 카드 생성](docs/configured-create.md)을 사용합니다.
별도 보드 정책 문서는 [읽기 전용 정책 검증](docs/policy-validation.md)으로 확인할 수 있습니다. 검증은 정책 활성화가 아닙니다.

## 개발과 라이선스

`make check`는 포맷·vet·race 테스트·빌드를 실행합니다.
테스트 데이터는 합성 예제입니다. 보안 문제는 공개 이슈에 비밀정보를 첨부하지 마세요.
MIT 라이선스이며 파생 코드의 원저작권 고지는 [LICENSE](LICENSE)에 보존합니다.
아직 릴리스 바이너리나 설치 자동화는 제공하지 않습니다.
