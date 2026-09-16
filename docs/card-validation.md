# 명시적 단일 카드 규격 검증

`validate FILE --json`은 기존과 같이 YAML/frontmatter 구문을 검사한다. 전체 보드의
ID·관계·실행권·완료 증거를 검사하는 명령이 아니다. `--config`를 명시하면 work TASK
카드의 문서 규칙도 검사한다. 어느 경로도 카드·설정을 수정하거나 본문 명령을 실행하지 않는다.

```sh
taskchain-task-manager validate tasks/todo/P4-example.md --config validation.yaml --json
```

```yaml
schema-version: 1
card-dialect:
  name: team-card
  id-required: false
  summary-heading: Summary
  criteria-heading: Acceptance Criteria
  priority-values: [P0, P4]
  task-types: [bug, audit]
  filename-prefixes: [P0, P4]
```

## 적용 규칙

명시하지 않은 필드는 기본값을 사용한다: ID 필수, Summary/Completion Criteria,
priority P0–P3, type feature/bug/chore/refactor/cleanup/docs/test, 파일명 prefix P0–P3.
`name`은 설명용이다. 필드를 지정했다면 빈 값·중복·잘못된 형식은 오류이며 기본값으로 숨기지 않는다.
schema-version과 card-dialect 블록은 필수다. 설정은 단일 YAML 문서다.

- ID·title·type·priority는 비어 있지 않은 문자열이다. ID 선택 모드에서는 id 자체를 생략할 수 있다.
  ID가 있으면 TASK 숫자 ID여야 한다. PLAN/ISSUE/BACKLOG 전용 규격 검증은 이 명령의 범위가 아니다.
- type과 priority는 선언된 집합에 속해야 한다. 본문의 제목으로 빠진 frontmatter title을 대신하지 않는다.
- 선언한 h2 Summary/criteria 제목을 검사한다. criteria 제목은 Acceptance Criteria,
  Completion Criteria, 완료 조건, 완료 기준 중에서 선택한다. 대소문자는 구분하지 않는다.
- criteria 구간의 checkbox 항목을 구조화해 반환한다. fenced 코드 예시는 제외한다.
  heading/항목 앞 0–3칸 공백을 허용하며 4칸 이상 또는 탭으로 들여쓴 코드 줄은 제외한다.
  `- [ ]`, `- [x]`, `- [X]`, `- [>]` 뒤에 비어 있지 않은 문장이 있어야 한다.
  `[>]`는 미완료 항목으로 반환한다.
  checkbox가 모두 체크돼도 구현 완료나 증거 검증 통과로 판정하지 않는다.
- 파일명은 숫자 또는 선언한 prefix로 시작하는 kebab-case 권고 형식이다.
  다른 파일명은 warning이며 그것만으로 valid=false가 되지는 않는다.

## 범위와 오류

결과에는 `schemaVersion: 1`, `scope: card`, `boardValidation: not_evaluated`,
`valid`, `criteria`, `findings`가 포함된다. finding은 severity/field/message를 가진다.
정상 검사 성공은 exit 0, 카드 규칙 위반은 exit 1과 JSON findings를 반환한다.
설정·파일·구문 오류는 exit 1과 stderr 진단만, 잘못된 명령 사용은 exit 2다.

`zones`, `zone-status`, `transitions`와 알 수 없는 설정은 **거부**한다. 보드·writer 정책을
평가하지 않은 채 성공한 것으로 표시하지 않는다. CE 설정 파일의 자동 탐색·runtime fallback은
없으며, `ce-tasks.yaml`을 그대로 전달하는 호환 명령도 아니다. 별도 명시적 설정을 사용한다.
다른 명령의 생성·상태 이동·ready 동작은 이 설정의 영향을 받지 않는다.

ID 없는 카드가 검증돼도 Task Manager lifecycle에서 사용할 수 있다는 뜻은 아니다.
현재 lifecycle은 안정적인 ID를 요구한다. 전체 CE 검증·증거 gate와 동일한 결과를 보장하지 않는다.
validate의 입력 카드는 최대 1 MiB, 설정은 최대 64 KiB의 일반 파일이며 symlink는 거부한다.
