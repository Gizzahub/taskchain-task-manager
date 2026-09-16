# 명시 규칙에 따른 work-card 생성

기본 `create`는 기존 최소 카드를 만든다. 검증 규칙에 맞는 work TASK 문서를 만들려면
`--config`와 문서 필드를 명시한다. 설정은 이 호출에만 사용하며 보드 정책으로 저장하지 않는다.

`validation.yaml` 예시:

```yaml
schema-version: 1
card-dialect: {}
```

```sh
taskchain-task-manager init --dir tasks --json
taskchain-task-manager create --dir tasks --title 'Inspect example' \
  --config validation.yaml --type bug --priority P1 \
  --summary 'Inspect a synthetic example' --criterion 'Record the result' --json
```

기본 규칙에 없는 audit/P4 등을 사용하려면 설정에서 해당 값을 명시적으로 허용한다.
허용하지 않은 값은 오류이며 카드·ID 예약은 생기지 않는다. 첫 enum 값으로 대체하지 않는다.

## 생성 계약

- `--type`, `--priority`, `--summary`, 하나 이상의 `--criterion`이 필요하다.
  criterion은 반복할 수 있고 모두 미완료 checkbox로 생성한다.
- 제목·type·priority·summary·각 criterion은 비어 있지 않은 단일 줄이다.
  CR/LF/NUL 및 잘못된 UTF-8은 오류다. 본문·검증 문자열을 실행하지 않는다.
- summary의 Markdown 구조 문자는 escape하여 상태 표·제목·코드 fence로 해석되지 않게 한다.
- `--kind task` 또는 기본 kind를 사용한다. PLAN/ISSUE/BACKLOG는 이 규칙으로 생성하지 않는다.
- `id-required: false`인 규칙도 **생성하는 작업에는 ID를 부여**한다. 선택적 `--id TASK-N`과
  `--depends-on TASK-N`은 기존 identity·의존성 검증을 그대로 따른다.
- YAML metadata와 Summary/criteria 구간, 상태 표를 생성하고 같은 규칙으로 검증한 뒤 게시한다.
  기준 미달·렌더링 오류·1 MiB 초과는 local/shared ID 예약과 카드 게시 전에 거부한다.
- 파일명은 기존 `todo/TASK-N.md`다. 파일명 규칙의 warning만으로 생성을 막지는 않는다.
  이미 성공한 create를 다시 호출하면 새로운 작업을 만들 수 있으므로 무조건 재실행하지 않는다.
- 카드 검증 설정이 보드의 claim·transition·완료 증거 정책을 바꾸지는 않는다.
  이후 상태 이동은 기존 소유권·journal 계약을 따른다.

`--config` 없이 profile 전용 옵션을 주면 사용 오류(exit 2)다. 잘못된 설정이나 문서 입력은
exit 1과 stderr 진단이며 성공 JSON은 출력하지 않는다. 기존 게시 단계 실패는 ID가 소비될 수
있다는 진단을 따르고 실제 파일을 확인한다. 설정 오류와 게시 중 오류는 구분해야 한다.
공유 모드의 잠금/metadata 준비 자체는 카드 입력 거부와 별개로 수행될 수 있다.

성공 후에는 반환된 `path`를 대상으로 같은 규칙으로 재검증할 수 있다:

```sh
taskchain-task-manager validate tasks/todo/TASK-1.md --config validation.yaml --json
```

이는 단일 카드의 규격 확인이며, TASK 완료나 Intent 달성의 증거가 아니다.
