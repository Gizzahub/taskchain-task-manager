# 파일 역할 검사 연결의 지원 범위

정책 schema 4는 보드 파일의 역할을 명시하는 선행 계약이다. `validate-policy`로
문서를 검증할 수 있다. task lint·gate의 validate/lint/preflight 단계와 task preflight가
authority-bound reader와 내부 scanner를 소비한다. list·추천·board view·named validation·
reconcile/cleanup 및 직접 writer의 전체 연결은 아직 완료되지 않았다. **현재 schema 4의 activate-policy·revise-policy는 계속 거부한다.**
일반 사용자 보드에 활성화할 수 있는 완성된 파일 역할 정책으로 취급하지 않는다.
정상·idle 상태의 기존 schema 1–3은 기존 파일 역할과 출력 형식을 유지한다.
연결된 명령은 기존 보드에도 authority 조회를 수행하므로 writer 잠금·손상되거나 고아가 된
엔진 metadata는 이제 exit 2 또는 gate unavailable로 거부한다. 이를 종전의 무잠금
조회와 같은 동시성 보장으로 해석하지 않는다.

```yaml
schema-version: 4
board-policy:
  modules: []
  file-roles:
    enabled: true
    projections: []
    guidance: [AGENTS.md]
    templates: []
```

```sh
taskchain-task-manager validate-policy policy.yaml --json
```

`file-roles`는 `board-policy` 아래에만 둔다. 세 목록은 빈 목록도 명시해야 한다.
README.md·INDEX.md를 기본 허용하지 않으며, guidance는 정확한 root AGENTS.md·
CLAUDE.md, template은 root TEMPLATE.md만 선언한다. 경로는 보드 root 기준 상대
경로다. glob·escape·별칭 경로는 받지 않는다. 선언은 파일 생성 지시가 아니다.

Projection은 README.md 또는 INDEX.md의 generator owner·revision·marker를
선언한다. marker는 owner와 revision 문자열을 포함해야 하며 scanner는 파일 첫 줄과
비교한다. owner·revision은 각각 최대 128 bytes, 앞뒤 공백 금지다. marker는 최대
512 bytes이며 세 값 모두 UTF-8 단일 줄, 제어문자 금지다.

현재 scanner는 생성 목록과 실제 카드 사이의 drift, 안내문·템플릿의 provenance를
완전히 검증하지 않는다. Guidance에는 정본 Skill 참조를 두고 정책 본문·카드 수·
ID 상한을 복사하지 않는다. source 테스트만으로 실제 runtime Skill loading 성공을
주장하지 않는다. 이 누락과 모든 writer의 admission·구버전 장벽·공유 복구 검증을
완료하기 전까지 새 정책 활성화 거부를 유지한다.

Scanner는 module·workflow·kind·기존 evidence 경계를 구분한다. 일반 zone에는 카드를
바로 두고 중첩 category는 선언한 module/zone 안에서만 허용한다. archive/_archive는
zone/kind 또는 YYYY-MM partition을 따른다. 미정의 파일·디렉토리·symlink·특수 파일은
구조 finding이며 Markdown 외 확장자와 선언된 파일의 실제 존재도 검사한다. 읽기 실패는
별도 error다. schema 4 gate는 구조 위반으로 안전한 카드 검증이 불가능하면 validate를
명시적으로 skipped로 기록하고 lint 실패로 보고한다. 기존 보드의 validate-first 순서는
유지한다.
카드 후보는 경로·확장자로 분류하며 본문·ID 검증은 카드 parser가 담당한다.

보드 root를 pinned handle로 검사한다. Projection은 그 root 안에서 no-follow 방식으로
열고 regular-file identity를 다시 확인해 제한된 첫 줄만 읽는다. no-follow open을
제공하지 않는 플랫폼에서는 projection 읽기를 거부한다. engine-private 상태는
`internal/boardstate`의 정확한 이름·타입 형식으로 분류한다. 숨김 prefix 전체를
허용하지 않으며, 이 분류는 해당 파일의 생성·소유·삭제 권한 증명이 아니다.
엔진과 evidence 제외 트리의 내부는 별도 기존 계약이 관리한다.

CLI는 raw 정책 파일의 존재를 활성화로 해석하지 않는다. Journal과 activation authority에
묶인 정책만 소비한다. 설정·authority·I/O 오류는 구조 위반과 구분한다. 정책 파일 복사나
journal 수동 편집으로 연결을 시험하지 않는다. 합성 테스트의 내부 fixture는 운영 채택
절차를 대신하지 않는다.

## 계획·Batch 문서의 역할 경계

미완료 실행의 계획·현재 상태·의존성·수용 기준·재개 지점은 채택한 보드의
`tasks/plan/` 또는 기존 plan/task 카드가 소유한다. 새 계획을 만들기 전에 같은
목표를 가진 기존 plan/task를 찾아 갱신하며, 계획 요청만으로 새 카드나 문서를
자동 생성하지 않는다. 계획을 명시적으로 기록할 때도 카드의 소유자·수명·검증
절차를 따른다.

지속되는 제품 방향·설계·결정은 소유 저장소의 결정 문서에 둔다. Intent·Batch·
Iteration context는 당시의 선택·범위·평가를 보존하는 불변 스냅샷이며, 현재
실행 상태나 재개 권한을 부여하는 live task source가 아니다. Batch에 실행 항목을
기록해야 한다면 관련 plan/task ID와 정확한 context revision을 참조하고, 현재
상태는 카드에서 읽는다.

분석·브리핑·임시 조사는 요청된 결과 형식으로 반환하며, 별도 저장을 명시적으로
요청하지 않은 한 task, plan, Batch 문서를 만들지 않는다. 사용자가 예외적인 저장
위치나 수명을 승인한 경우에는 승인 범위와 이유를 해당 정본에 남긴다.

이 경계는 문서의 즉시 이동·삭제를 뜻하지 않는다. 이 계약을 명시적으로 채택하지
않은 board와 context는 기존 계약과 writer 동작을 유지하며, legacy 기록을 자동으로
이동·삭제·재분류하지 않는다. 채택 뒤의 이전·정리·retention은 별도 owner 승인과
preview, 복구 가능한 writer 절차를 거치며 자동 migration이나 자동 deletion은
제공하지 않는다.

임시 참조 등록·cleanup, AGENTS 배포, 기존 보드 adoption preview와 실제 writer 전환은
여전히 별도 범위다. Scanner는 파일을 생성·이동·삭제하거나 보드 권한을 활성화하지
않는다. 최종 활성화는 완성된 provenance·drift 계약과 모든 소비자 검증 뒤 기존
authority·digest CAS·lock·pending recovery 절차로 제공해야 한다.
