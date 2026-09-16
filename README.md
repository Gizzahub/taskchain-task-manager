# TaskChain Task Manager

개발자와 코딩 에이전트를 위한 파일 기반 태스크 관리 도구입니다.
현재는 초기 개발 단계이며 카드 codec과 **초기화·생성·목록·조회·구문 검증**을 제공합니다.
claim·의존성 처리·Intent/Batch·자동 실행은 아직 제공하지 않습니다.

## 빌드와 실행

Go 1.26.6 이상이 필요합니다. 서버, 계정, 외부 에이전트 도구는 필요하지 않습니다.

```sh
make check
./build/taskchain-task-manager show examples/card.md --json
./build/taskchain-task-manager validate examples/card.md --json
./build/taskchain-task-manager init --dir ./tasks --json
./build/taskchain-task-manager create --dir ./tasks --title '첫 작업' --json
./build/taskchain-task-manager list --dir ./tasks --json
```

`validate`는 YAML frontmatter의 구문·형식을 검사합니다. 완료 조건, 의존성,
실제 구현의 정확성이나 작업 완료 여부를 판정하는 명령은 아닙니다.
카드 내용의 명령을 실행하지 않습니다. `show`·`validate`는 읽기 전용이고,
`init`·`create`는 지정한 보드에 새 디렉터리·카드를 만듭니다.

## 생성·목록의 안전 경계

- `create`는 `todo/TASK-N.md`에 새 카드를 만듭니다. 기존 파일을 덮어쓰지 않습니다.
- `--id TASK-N`을 생략하면 현재 보드·archive의 ID를 바탕으로 자동 할당합니다.
  삭제된 파일의 Git 이력은 조회하지 않으므로, 삭제된 ID의 영구 예약은 아직 지원하지 않습니다.
- `list`는 알려진 workflow·종류·archive 디렉터리를 검사하고 경로순 결과를 반환합니다.
  ID 중복·잘못된 카드·symlink를 발견하면 조용히 건너뛰지 않고 실패합니다.
- 지원 대상은 TASK-N 카드입니다. 기존 도구의 모든 카드 종류·dialect와 호환된다는 뜻은 아닙니다.
- 목록과 생성은 보드의 `.task-manager.lock`을 사용합니다. 다른 프로세스의 잠금이 있으면
  즉시 실패하며 자동으로 강제 해제하지 않습니다. 중단 뒤 잠금이 남으면 실행 중인 작업이
  없는지 확인하고 복구를 판단해야 합니다. 잠금이 있다는 이유만으로 삭제하지 마세요.
- 생성은 같은 filesystem의 임시 파일을 완성한 뒤 hard link로 공개합니다.
  hard link를 지원하지 않는 filesystem은 오류를 반환합니다. 전원 손실·네트워크 filesystem·
  잠금을 무시하는 외부 편집기까지의 일관성을 보장하지 않습니다.
- 생성 후 출력이나 정리 단계가 실패하면 카드가 이미 존재할 수 있습니다.
  재시도 전에 `list`와 대상 파일을 확인하세요. 실행 중에는 보드를 외부에서 편집하지 마세요.

## 출력 계약 (초기, 안정화 전)

- 성공: exit 0, stdout에 JSON 한 개. `validate`는 `{"valid":true}`.
- 파일/구문/출력 오류: exit 1, stderr에 원인, 입력 오류 시 stdout은 비어 있음.
- 잘못된 명령/인자: exit 2. `--help`는 exit 0.
- `show`는 알려진 메타데이터의 view이며 전체 원본 문서가 아닙니다.
- `tasks/` 아래 workflow 경로의 상태가 frontmatter보다 우선합니다. 다른 경로에서는
  frontmatter 상태를 사용합니다. 원본 파일은 수정하지 않습니다.
- 잘못되거나 끝나지 않은 YAML은 오류입니다. 이를 조용히 무시하지 않습니다.

내부 codec은 원본 byte를 보존하고 본문의 첫 Status 셀만 변경할 수 있습니다.
상태 patch는 아직 CLI나 안정적인 외부 Go API로 제공하지 않습니다.
알 수 없는 필드는 파생 view에서 생략되지만 원본 byte에는 보존됩니다.
Git 연동·기존 카드 갱신·분산 잠금은 아직 지원하지 않습니다.

## 개발과 라이선스

`make check`는 포맷·vet·race 테스트·빌드를 실행합니다.
테스트 데이터는 합성 예제입니다. 보안 문제는 공개 이슈에 비밀정보를 첨부하지 마세요.
MIT 라이선스이며 파생 코드의 원저작권 고지는 [LICENSE](LICENSE)에 보존합니다.
아직 릴리스 바이너리나 설치 자동화는 제공하지 않습니다.
