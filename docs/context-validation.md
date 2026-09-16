# Intent/Batch/Iteration 문서 검증

`validate-context FILE --json`은 독립적인 Intent, Batch 또는 Iteration JSON 문서 하나만
검증합니다.

```sh
taskchain-task-manager validate-context examples/context/intent.json --json
```

성공 결과는 `schemaVersion`, `scope: "intent-batch-document"`, `valid`, `kind`,
`id`, `revision`, `canonical`, `digest`, `registered: false`,
`referenceValidation: "not_evaluated"`, `evaluationValidation: "not_evaluated"`를
포함합니다. canonical은 고정 필드 순서의 compact JSON이며 digest는 그 바이트의
SHA-256입니다. Iteration의 scope는 `iteration-document`이며 출력 envelope의
schemaVersion은 여전히 1입니다. 문서 내부의 schemaVersion과 구분하세요.
배열 순서와 TASK ID의 원래 표기를 보존하며 필드 순서·들여쓰기는 정규화합니다.
같은 kind/id/revision은 불변 내용을 뜻하지만 이 단일 파일 검사는 기존 등록과의 충돌을
확인하지 않습니다. Intent/Batch 수정은 새 revision, Iteration 수정은 새 ID로 기록합니다.
저장·등록은 별도 [context registry](context-registry.md) 명령을 사용합니다.

이 명령은 파일 하나만 읽고 보드·Git·등록 원장·권한·실제 목표 달성을 조회하지
않습니다. Batch의 TASK가 존재하는지나 evaluation 증거가 진짜인지도 판단하지
않습니다. 성공한 문서는 저장하거나 자동 실행하지 않습니다.

문서는 UTF-8 strict JSON 하나여야 하며 최대 256 KiB입니다. 알 수 없는 필드,
중복 key, null, 잘못된 타입, 잘못된 Unicode escape, 복수 JSON 값은 오류입니다.
JSON 깊이는 16 이하이고 배열은 128개 이하입니다. 일반 문자열은 16 KiB 이하,
title은 256 bytes 이하, actor는 128 bytes 이하입니다. 본문 텍스트의 개행·탭을
제외한 제어문자, 빈 값, 바깥 공백은 허용하지 않습니다.

Intent는 `schemaVersion: 1`, `kind: "intent"`, `id`, `revision`(1..4294967295), `title`,
`outcome`, `mode: "completion"`, `constraints`, `nonGoals`,
`successCriteria`가 필요합니다. ID는 `INTENT-` 뒤 소문자 hex 32자이며 성공
기준은 하나 이상이어야 하고 key는 소문자 영문으로 시작하는 1..64자의
영문·숫자·하이픈이며 중복될 수 없습니다.

Batch는 `schemaVersion: 1`, `kind: "batch"`, `id`, 양의 `revision`,
`intent`(id/revision/digest), `gap`, `taskIds`, `constraints`,
`authorizationRefs`가 필요합니다. ID는 `BATCH-` 뒤 소문자 hex 32자이고,
`taskIds`는 하나 이상의 `TASK-N`이며 숫자 alias 중복도 거부합니다. 선택적인
evaluation은 actor·같은 intent 참조·decision·reason·remainingGaps·evidenceRefs를
포함하며 decision은 `achieved|continue|blocked|revise`입니다. `achieved`는
남은 gap이 없고 evidence reference가 하나 이상이어야 합니다.
Batch revision과 Intent 참조 revision도 1..4294967295입니다. 참조의 digest는 Intent
canonical bytes의 SHA-256 소문자 hex 64자입니다. 성공 기준의 각 항목은 `key`와 `text`를
포함합니다. `constraints`, `nonGoals`, `authorizationRefs`, `remainingGaps`, `evidenceRefs`는
문자열 배열이며 빈 배열은 허용합니다. evaluation을 제외한 필드는 모두 필수입니다.
authorizationRefs는 실제 실행 승인이 아니며 actor는 인증된 신원이 아닙니다.
schemaVersion 2의 유지형 Intent와 Iteration은 [유지형 관측 기록](maintenance-iterations.md)을
따릅니다. 자동 반복·스케줄러는 제공하지 않습니다.

잘못된 입력은 stdout 없이 exit 1을 반환합니다. 잘못된 인자에는 exit 2,
`--help`에는 exit 0을 사용합니다.
