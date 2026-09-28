# 큐의 실행 경로 분류

`queue`는 준비된 작업 카드와 P0 issue를 읽어 실행 경로를 표시합니다. 카드를
이동·완료 처리하거나 카드 본문의 명령을 실행하지 않습니다. 반환된 경로와
`needsHuman`은 서로 다른 질문에 답합니다. 경로는 어떤 종류의 후속 처리인지,
`needsHuman`은 에이전트가 직접 실행할 수 있는지 나타냅니다.

| `execution-mode` | `needs-human` | `allowed-paths` | 큐에서의 의미 |
|---|---|---|---|
| `implementation` | `false` 또는 `true` | 비어 있지 않은 정확한 상대 경로 목록 | 범위가 선언된 구현. `true`이면 사람 전용 |
| `external` | `true` | 선언하지 않음 | 저장소 밖의 후속 조치를 사람에게 전달 |
| `decision` | `true` | 선언하지 않음 | 사람이 선택해야 하는 결정을 전달 |

`execution-mode`를 생략하면 `implementation`입니다. `needs-human: true`만으로
카드를 `external`이나 `decision`으로 추정하지 않습니다. 다음은 저장소 밖의
조치가 필요한 카드의 frontmatter 예입니다.

```yaml
---
id: TASK-17
title: 외부 서비스 확인
execution-mode: external
needs-human: true
---
```

구현 카드는 `allowed-paths`에 정확한 저장소 상대 경로를 하나 이상 선언합니다.
`tasks/` 보드 내부, 절대 경로, 부모 탐색, glob은 구현 범위로 허용하지 않습니다.
플랫폼에 따라 의미가 달라지는 Windows 드라이브 표기(`C:/...`)와 역슬래시,
공백·제어 문자도 거부합니다.
이 검사는 경로 문자열의 범위만 검사하며 대상 파일의 존재나 심볼릭 링크를
검증하지 않습니다.
`external`·`decision` 카드에 가짜 경로를 선언하거나 빈 목록을 넣는 것도
거부합니다. `execution-mode`·`allowed-paths`의 잘못된 형식은 공통 카드 파서가
거부하고, 실행 경로와 범위의 잘못된 조합은 `queue`가 전체 결과를 거부합니다.

`queue --dir BOARD --json`의 각 `runnable` 항목에는 기존 `needsHuman`과 함께
`executionMode`, `allowedPaths`가 출력됩니다. `allowedPaths`는 선언이 없는
외부·결정 카드에서도 빈 배열입니다. `agentRunnable`에는 `needsHuman: false`인
적격 구현 카드만 들어갑니다. P0 issue는 별도 경로로 관찰되며 P1 issue를
자동 승격하지 않습니다. issue의 활성 상태는 `todo`, `open`, `pending`,
`in-progress`/`in_progress`/`doing`, `review`, `blocked`로 한정합니다.
`done`, `superseded`, `cancelled` 및 알 수 없는 상태는 큐에 올리지 않습니다.
`ready`와 claim의 기존 작업 선택 의미는 유지됩니다.

읽기 전용 분류는 종료 상태를 결정하거나 외부 작업이 실제 수행됐음을 증명하지
않습니다. 큐의 우선순위와 서로 다른 경로 사이의 한 항목 자동 선택도 이 명령의
계약이 아닙니다.
