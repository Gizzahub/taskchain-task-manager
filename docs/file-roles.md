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

## Cards-only snapshot foundation

Internal snapshot assessor가 명시 선택한 입력의 카드 전용 구조를 판정한다.
새 policy schema·활성화 신호를 만들지 않으며 기존 schema 1–5 reader와 명령은 보존한다.
카드 경로·디렉토리와 정확한 root `README.md`를 허용하고 예약 비카드 이름·증거/참조/결정
디렉토리·엔진 상태는 제외하지 않고 보고한다. Manual은 경로 overlay일 뿐 상태·actor·
receipt admission을 구현한 것이 아니다.

`NewCardsOnlySnapshot`은 caller가 제공한 path/mode/tracked/bytes pair를 복사한다.
Source label과 입력의 일관성·완전성은 caller 주장이다. 복사는 이후 입력 변경과 snapshot을
분리할 뿐 Git 상태나 안정된 물리 filesystem을 capture·인증하지 않는다. 중복·이탈·비UTF-8/
control 경로·모순 ancestor/mode·빈 tree 디렉토리는 입력 unavailable로 거부한다. Symlink/
special은 카드가 될 수 없다. Missing은 bytes 없는 tracked regular worktree leaf만 나타내며
projection 비교를 막는다.

Tracked worktree README projection은 supplied tracked 카드 경로와 supplied worktree README
bytes를 비교한다. Residue README는 supplied physical 카드 목록과 비교하고 advisory로 남는다.
Index/ref는 각 source로 제공된 entries/bytes만 사용한다. 기존 marker 문법과 size limit을
공유한다. Legacy `.ce`는 진단 하나로 접지만 실제 비순회 capture는 제공하지 않는다.

같은 internal finding을 lint 구조 error/residue warning 또는 validate warning으로 변환한다.
이름 지정 검사는 pruned ancestor finding도 포함한다. 아래 격리 후보 진단 CLI가 capture와
assessment를 호출하지만 board ready 판정을 보장하지 않는다. 공개 채택 wire·기존 CLI/gate/
writer 연결·활성화·cleanup·migration은 이 foundation의 지원 범위 밖이다.

## Inactive cards-only capture

명시 internal reader는 ref를 tree OID로 고정하고, index는 stage0 mode·blob OID·경로와
flags 목록의 SHA-256으로 식별한다. Worktree는 index A → 고정한 디렉토리의 두 관측 →
index B 순서로 읽는다. 목록·내용·파일 identity의 관측된 변경은 전체 오류다. Index digest는
worktree 전체 digest나 index tree OID가 아니다. Git 객체를 만들거나 index를 쓰지 않는다.

이 수집은 원자적 스냅샷·동시 변경의 완전한 탐지·writer admission 증거를 보장하지 않는다.
이름·mode·출처를 임의로 전달하는 기존 constructor와 구분된다. 아래 격리 후보 진단 CLI가
호출하며 authority·gate·writer·adoption caller는 아니다.
Git 환경 override, conflict·intent-to-add·skip-worktree·gitlink·미지원 mode는 거부한다.
Git tree/index의 빈 inventory는 물리 보드 디렉토리 존재의 증명이 아니다.

Unix 물리 수집은 구성요소와 파일을 no-follow로 열며 symlink/special 내용을 읽지 않는다.
`.ce/`와 보드 내부 `.git/` 디렉토리는 metadata 하나로 접고 열거나 순회하지 않는다. Git descendant metadata는
그 parent의 추적 등급으로 결속한다. Tracked regular 삭제만 Missing으로 나타내며 tracked
symlink 삭제는 unavailable이다. 미지원 OS의 physical 수집도 unavailable이다.

파일 payload1MiB, 전체 payload64MiB, inferred directory 포함100000 entry, path64KiB를
넘으면 전체 오류다. Retained path 총합16MiB, Git listing 단계별16MiB와 stderr64KiB는 별도 한도이며 process 전체
64MiB를 보장하지 않는다. Ignored와 untracked 파일은 모두 physical residue 대상이다.
Active 정책 선택 wire·기존 CLI/gate/writer 연결·보드 채택·활성화·정리·설치는 여전히 별도 범위다.

물리 reader는 읽는 regular 파일의 pathname no-follow identity를 열린 FD와 읽기 전후 직접 대조한다.
순회하는 디렉토리는 반복 worklist에서 목록 읽기 전후 고정 root 기준으로 다시 열며, 각 조상을 관측 identity와
대조하므로 열린 디렉토리 FD는 깊이에 비례해 늘지 않는다. Opaque `.ce/.git`은 기존대로 열지 않고
no-follow pathname metadata만 관측한다. 재열기 비용은 디렉토리 깊이의
합에 비례하며 깊은 트리의 CPU 비용을 상수로 보장하지 않는다. 기존 경로·entry·metadata 한도는
유지하고 새 깊이 제한은 추가하지 않는다. 공유 부모 metadata는 중복 순회를 줄여 처리한다.

취소는 root 구성요소·디렉토리 배치·파일 read 전후·metadata 처리에서 확인한다.
이미 시작된 OS read와 정렬 자체를 즉시 중단한다는 보장은 없다. 취소·identity 불일치는
부분 snapshot 없이 오류로 반환하며 열린 FD를 회수한다. 이 보강도 일시적 ABA 전체 탐지나
원자성·mutation 권한을 보장하지 않는다.

## 명시 후보의 읽기 전용 진단

`assess-cards-only`는 외부 후보 정책과 선택한 단일 Git 출처를 비교한다.
모든 입력은 명시해야 하며 기본 정책·현재 active authority로 돌아가지 않는다.

```text
taskchain-task-manager assess-cards-only --repo ROOT --board PATH
  --candidate-policy FILE --source worktree|index|ref [--ref REV] --json
```

ROOT는 실제 Git worktree 루트, PATH는 비루트 repository-relative 보드 경로다.
상대 ROOT/FILE은 호출 cwd 기준이며 FILE은 index/ref blob이 아닌 현재 파일시스템의
prospective 입력이다. Source가 ref일 때만 REV를 지정한다. 각 옵션은 정확히 한 번,
`--json`은 값 없는 단독 flag로 쓴다. 단독 `--help`는 사용법을 출력한다.

후보 파일은 경로의 symlink·특수 파일·identity 교체를 거부하고 최대64KiB로 읽는다.
안전한 후보 읽기가 지원되지 않는 OS는 Git-only 출처에서도 unavailable이다.
후보가 보드 안에 있으면 해당 출처의 일반 entry로 검사하며 파일명 면제를 주지 않는다.
완전한 inventory에서 선언된 README projection이 없으면 구조 finding으로 보고한다.

JSON의 `outputVersion`은1, `mode`는 `candidate-diagnostic`, `authorityChecked`는
항상 false다. Candidate에는 원본/정규화 정책 SHA-256과 codec schema를 구별해 담고,
snapshot에는 ref의 treeOID 또는 index/worktree의 indexDigest를 담는다.
`clean`은 제한된 구조 진단의 finding이 없다는 뜻이다. 카드 내용·의존성·manual 수명,
현재 authority 정상성·채택 가능성·readiness·writer 권한을 증명하지 않는다.

- Exit0: clean. Exit1: findings; residue만 있어도1이다.
- Exit2: 후보·수집·판정 불가 또는 취소. 정상 문법의 실패는 unavailable JSON이며
  부분 findings를 반환하지 않는다. 문법 오류는 stdout 없이 stderr에 사용법을 출력한다.
- Stdout 쓰기 실패는 exit2이며 부분 JSON이 남을 수 있다. 종료 코드와 출력 완전성을
  함께 확인해야 한다. 후보 원문·절대 입력 경로는 오류 출력에 반사하지 않는다.

이 명령은 active authority 읽기·Store 생성·lease·ID 예약·journal 복구·파일 변경을
호출하지 않는다. 기존 lint/validate/gate의 정책·출력·종료 코드는 유지하며, 이 진단 결과를
정식 adoption preview나 gate evidence로 대신 사용할 수 없다.

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
