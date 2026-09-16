# 완료 조건 관측

`validate-completion FILE --config RULES --json`은 카드 파일 하나의 구조와 완료 조건
체크박스를 읽습니다. 명시적인 카드 규칙 파일이 필수이며 보드 정책을 자동 탐색하지 않습니다.

```sh
taskchain-task-manager validate-completion examples/completion/P1-observation.md \
  --config examples/completion/validation.yaml --json
```

규칙 형식·입력 제한은 [카드 검증](card-validation.md)과 같습니다. 일반 `validate`의
구문 검사 또는 `validate --config`의 카드 구조 검사를 바꾸지 않습니다.

## JSON 해석

- schemaVersion은 1, scope는 `card-completion-observation`입니다.
- cardValid는 명시 규칙에 대한 기존 카드 구조 검사 결과입니다.
- criteriaComplete는 완료 조건이 하나 이상 있고, 지원하는 문법이며, 모두 checked인 경우에만 true입니다.
  메타데이터 오류와 별도로 계산하므로 cardValid=false, criteriaComplete=true도 가능합니다.
- valid는 cardValid와 criteriaComplete가 모두 true인 경우입니다.
- criteria에는 text/checked 관측값, findings에는 severity/field/message가 담깁니다.
- evidenceValidation과 boardValidation은 항상 `not_evaluated`입니다.

정상 파싱된 미완료·규칙 위반 카드는 JSON과 exit 1을 반환합니다. 성공은 exit 0입니다.
읽기·구문·규칙 설정 오류는 stdout 없이 stderr와 exit 1, 잘못된 인자는 exit 2입니다.
출력 도중 오류도 exit 1이며 부분 출력이 있을 수 있습니다.

## 지원 문법과 안전 경계

선택한 criteria h2 구간의 `- [x] 내용`, `- [X] 내용`은 checked입니다.
`- [ ] 내용`과 `- [>] 내용`은 미완료입니다. 미지원 bullet(`*`, `+`, 번호) 또는
bare bracket 형태를 체크박스로 적으면 완료 관측은 거부합니다. 지원 문법과 혼합해도
미지원 항목을 조용히 생략하지 않습니다. 목록 안의 들여쓴 checkbox는 하위 작업일 수
있어 거부합니다. 목록 안 코드/중첩 fence가 모호하면 top-level fenced 예제로 옮기세요.
완전한 Markdown 해석기가 아닌 보수적인 관측입니다. 코드 fence와 독립 들여쓰기 코드, 다른 h2 구간은
완료 조건으로 세지 않습니다. 아무 criteria도 없는 문서는 완료로 인정하지 않습니다.

이 명령은 파일·카드 상태·claim·journal을 수정하지 않고, 본문의 명령이나 evidence 링크를
실행하지 않습니다. `done/` 경로나 체크 표시는 실제 구현·테스트·권한의 증명이 아닙니다.
CE gate block과 runtime receipt의 진위, Git 통합, 보드 전체 의존성을 검사하지 않습니다.
따라서 valid=true를 CE gate 통과·전이 승인·실제 업무 완료로 해석하지 마세요.
`transition`의 상태 규칙도 이 명령 때문에 바뀌지 않습니다.
