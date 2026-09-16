# TaskChain Task Manager

개발자와 코딩 에이전트를 위한 파일 기반 태스크 관리 도구입니다.
현재는 초기 개발 단계이며 **카드 조회·구문 검증 codec만 제공**합니다.
생성·claim·의존성 처리·Intent/Batch·자동 실행은 아직 제공하지 않습니다.

## 빌드와 실행

Go 1.26.6 이상이 필요합니다. 서버, 계정, 외부 에이전트 도구는 필요하지 않습니다.

```sh
make check
./build/taskchain-task-manager show examples/card.md --json
./build/taskchain-task-manager validate examples/card.md --json
```

`validate`는 YAML frontmatter의 구문·형식을 검사합니다. 완료 조건, 의존성,
실제 구현의 정확성이나 작업 완료 여부를 판정하는 명령은 아닙니다.
현재 명령은 파일을 읽기만 하며 카드 내용의 명령을 실행하지 않습니다.

## 출력 계약 (초기, 안정화 전)

- 성공: exit 0, stdout에 JSON 한 개. `validate`는 `{"valid":true}`.
- 파일/구문/출력 오류: exit 1, stderr에 원인, 입력 오류 시 stdout은 비어 있음.
- 잘못된 명령/인자: exit 2. `--help`는 exit 0.
- `show`는 알려진 메타데이터의 view이며 전체 원본 문서가 아닙니다.
- `tasks/` 아래 workflow 경로의 상태가 frontmatter보다 우선합니다. 다른 경로에서는
  frontmatter 상태를 사용합니다. 원본 파일은 수정하지 않습니다.
- 잘못되거나 끝나지 않은 YAML은 오류입니다. 이를 조용히 무시하지 않습니다.

내부 codec은 원본 byte를 보존하고 본문의 첫 Status 셀만 변경할 수 있습니다.
이 기능은 아직 파일 쓰기 CLI나 안정적인 외부 Go API로 제공하지 않습니다.
알 수 없는 필드는 파생 view에서 생략되지만 원본 byte에는 보존됩니다.
Git 연동·동시 쓰기·원자적 저장을 지원한다고 해석하지 마세요.

## 개발과 라이선스

`make check`는 포맷·vet·race 테스트·빌드를 실행합니다.
테스트 데이터는 합성 예제입니다. 보안 문제는 공개 이슈에 비밀정보를 첨부하지 마세요.
MIT 라이선스이며 파생 코드의 원저작권 고지는 [LICENSE](LICENSE)에 보존합니다.
아직 릴리스 바이너리나 설치 자동화는 제공하지 않습니다.
