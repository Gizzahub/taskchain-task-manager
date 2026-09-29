# CE 패리티 fixture 스위트

CE `ce task` 참조 엔진(고정 커밋 `bb970b24`)의 관찰된 동작을 fixture expected로
고정하고, 제품(taskchain-task-manager)이 그 계약에서 얼마나 떨어져 있는지
측정한다. 근거: ce-agent-kit ADR-0050~0055와
`docs/00-product/11-task-migration-ledger.md`의 네 묶음 정의.
expected는 사람이 쓰지 않는다 — 참조 바이너리를 실제로 실행해 캡처한다.

## 참조 고정

- 참조 바이너리: `CE_BIN`(기본 `ce`), 빌드 문자열이 `PARITY_PIN`(기본
  `bb970b24`)을 포함해야 한다. 어긋나면 캡처·검증을 거절한다(다른 기준값에
  캡처한 expected를 망가뜨리지 않기 위함).
- `PARITY_PIN=unpinned`이면 pin 검사를 건너뛴다(로컬 실험 전용).

## 레이아웃과 계약

```
fixtures/ce-parity/<bundle>/<name>/
  fixture.sh   매니페스트 — runner가 source한다
  board/       입력 보드 트리(그대로 임시 workspace로 복사 후 git init)
  expected/    capture가 기록: exit-code, stdout, stderr, tree.txt
```

fixture.sh가 선언하는 것:

| 변수 | 의미 |
|---|---|
| `PARITY_CMD=()` | 실행할 `ce task <argv>` |
| `PARITY_TASKS_DIR` | 설정 시 `TASKS_DIR` 환경변수로 전달(decisions 묶음) |
| `PARITY_SNAP_GIT_CE=1` | 결과 트리에 `.git/ce/**` 포함(카드 ID 원장) |
| `PARITY_SETUP=''` | 실행 전 workspace 안에서 eval할 준비 명령 |

## 무엇을 parity로 보지 않는가 (정규화 선언)

캡처와 비교 양쪽에 같은 프로그램(`scripts/parity.sh`의
`write_normalizer_program`)을 적용한다. 선언된 것은 이 네 가지뿐이다:

- 임시 workspace 절대경로 → `<WS>`(CE가 filepath.Clean한 단일 슬래시 형태로 접어 규칙과 맞춘다)
- `created:`/`created-at:` 날짜 → `<DATE>`
- `resolved-at:` 타임스탬프 → `<TIMESTAMP>`
- `quality-reviewed-at:` → `<DATE>`

`tree.txt`는 workspace 전체 파일의 정규화 내용 sha256 목록이다(정렬됨).
`.git`은 제외하되 `PARITY_SNAP_GIT_CE=1`이면 `.git/ce/**`는 포함한다.

실행 날짜와의 독립은 실측으로 확인했다: 이 스위트가 다루는 명령면에서 CE가
새로 쓰는 유일한 날짜는 카드의 `created:`(위 규칙으로 무효화)이고,
`list --json`의 `"created"`는 입력 보드에 고정된 값을 에코하는 것이라
휘발하지 않는다. 새 명령을 범위에 넣을 때 CE가 쓰는 날짜 키를 다시 실측하고
여기에 선언한다.

## 사용법

```bash
bash scripts/parity.sh capture [bundle/fixture ...]   # CE 실행으로 expected 기록
bash scripts/parity.sh verify  [bundle/fixture ...]   # CE 재실행, 하나라도 미세차면 exit 1
make parity-ce                                        # verify와 같다
make parity-report                                    # 제품 측정 + 보고서
```

- 위치인자 selector는 fixture 경로의 **하위 문자열**로 일치한다(`verify list`
  는 list-text와 list-json을 모두 고른다). 하나도 일치하지 않으면 exit 2로
  중단하고 아무것도 실행하지 않는다.
- `PARITY_KEEP=1`은 마지막 실행 workspace를 `<현재 디렉터리>/build/parity-workspace`
  에 보존한다(디버깅용, 기본 꺼짐).
- report는 `PARITY_REPORT_DIR`(기본 `<현재 디렉터리>/build/parity-report`,
  `make parity-report`는 제품 루트 `build/parity-report`)의 `REPORT.md`에
  묶음별 요약과 fixture별 미세차 첫 원인을 남긴다. 측정 대상은 `PRODUCT_BIN`
  환경변수로 지정한다(`make parity-report`는 make build 산출물을 쓴다).
  미세차가 난 fixture의 실제 산출물만 `actual/<fixture>/` 아래 보존된다 —
  통과한 fixture의 사본은 남지 않는다.

## 묶음과 의도 (28 fixtures)

- **card-core** (9): validate 통과/이미-통과-거절, list 텍스트·JSON, new
  스캐폴드+ID 예약, move 상태 동기화, move 인바운드 살아있는 링크 재조준
  (ADR-0054), archive 편입/미완료 거절.
- **id-reservation** (2): 원장 바닥이 본문 인용 id가 아니라 frontmatter·원장에서
  온다(ADR-0050), 999 천장에서의 수 공간 포화 거절.
- **validation-gate** (14): 별칭 헤딩 경고(ADR-0051), 바인딩 없는 체크박스
  오류, 자리표시자 경고, 기준 섹션 부재 오류, 상태 캡처 세 모양(ISSUE-077 —
  `rc=$?`와 `out=$(cmd) || exit 1`는 인정, 잘린 `;` 캡처는 오류), 존 경로 인용
  진단(ISSUE-055), lint 파일명-id 정합 오류, doing-zone 경고, gate 체크 바인딩
  재실행 실패(텍스트·JSON, ADR-0052), 빈 보드·tasks 부재의 측정 불가(exit 2),
  plan 카운터 명부 계약(ADR-0053), preflight --json.
- **decisions-validate** (3): `TASKS_DIR=decisions`에서 결정 문서+README 색인
  통과, 필수 섹션 누락 오류, 색인 표 누락 오류.

## 알려진 미범위

- gate 바인딩 재실행의 30초 시간초과 경로(측정이 아니라 시간이 지배)
- `change-id`·`gate-evidence`(커밋/브랜치 상태에 의존해 결정적으로 고정 불가)
- 참조 스캔 바닥(`WithRefScan`)의 저장소 전체 인용 증거 — 보드 밖 파일을
  범위로 두면 fixture가 저장소 크기가 된다
- `ce-tasks.yaml` 방언 선언(확장 zone/전이) 경로
