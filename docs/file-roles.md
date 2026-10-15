# 파일 역할 reader의 지원 범위

정책 schema 4는 보드 파일의 역할을 명시하는 **읽기 전용 선행 계약**이다.
현재 `validate-policy`로 문서를 검증할 수 있다. 공통 구조 scanner는 내부 API로만 제공한다.
CLI `lint`·`gate`와 모든 writer의 admission 연결은 후속 범위이며, 새 정책의
`activate-policy`·`revise-policy`는 파일 변경 전에 오류로 거부한다.
기존 schema 1–3 보드의 정책·lint 동작은 그대로다.

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
README.md·INDEX.md를 기본 허용하지 않으며, guidance는 정확한 root `AGENTS.md`·
`CLAUDE.md`, template은 정확한 root `TEMPLATE.md`만 선언한다. 경로는 보드 root
기준 상대 경로이고 glob·escape·별칭 경로를 받지 않는다. 선언은 파일 생성 지시가 아니다.

Projection은 정확한 README.md 또는 INDEX.md에 generator owner·revision·marker를
함께 선언한다. marker는 owner와 revision 문자열을 모두 포함해야 하며 scanner는
파일 첫 줄과 marker를 비교한다. owner·revision은 각각 최대 128 bytes이고 앞뒤
공백을 허용하지 않는다. marker는 최대 512 bytes다. 세 값 모두 UTF-8 단일 줄이며
제어문자를 허용하지 않는다. 수동 카드 수·ID
상한을 별도 정본으로 운영하지 않는다. Guidance에는 정본 Skill 참조만 두며 정책 본문이나
현재 카드 목록을 복사하지 않는다. 현재 reader 추가만으로 실제 runtime의 Skill loading이
검증된 것은 아니다.

Scanner는 선언된 module·workflow·kind·기존 evidence 경계를 구분하고 알 수 없는
파일·디렉토리·symlink를 구조 오류로 반환한다. 일반 zone에는 카드를 바로 두며,
중첩 category는 선언한 module/zone 안에서만 허용한다. 보관 영역은 기존 zone/kind 또는
YYYY-MM partition을 따른다. 확장자가 Markdown이 아니어도 검사한다.
파일 읽기 실패는 구조 오류와 구분한 error다. engine-private 트리는 내부 포맷을 재해석하지
않는다. 안정적인 엔진 상태 경로는 명시 목록으로 분류하며, 동적 pending artifact
경로와 상태 경로 정본의 공통화는 writer 연결 전 후속 작업이다.
카드 후보는 경로와 확장자로 분류하며 본문·ID 유효성 검사는 기존 카드 parser의 책임이다.
어느 경우에도 scanner는 파일을 생성·이동·삭제하거나 보드 권한을 활성화하지 않는다.

임시 참조 등록·cleanup, plan 문서 라우팅 Skill, 기존 보드 adoption preview와 all-writer
전환은 이 선행 구현의 지원 범위에 포함되지 않는다. 새 정책 파일을 보드에 복사하거나
기존 authority 기록을 손으로 수정해서 적용하지 않는다. 활성화는 writer 호환 구현과
별도 채택 검증 후 기존 authority·digest CAS·lock·복구 절차를 통해 제공해야 한다.
