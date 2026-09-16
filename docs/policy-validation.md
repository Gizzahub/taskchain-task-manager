# 보드 정책 문서 검증

`validate-policy`는 명시한 정책 문서 하나를 읽고 검증한다. 보드를 찾거나 설정을 설치하지
않는다. 이 명령으로 유효성을 확인해도 create/claim/transition에 적용되지 않는다.

```yaml
schema-version: 1
board-policy:
  zones: [manual]
  zone-status:
    manual: blocked
  transitions:
    - from: manual
      to: [todo]
    - from: todo
      to: [doing]
```

```sh
taskchain-task-manager validate-policy policy.yaml --json
```

## 문서 계약

- root의 `schema-version: 1`과 `board-policy` mapping은 필수다. `{}`는 기본 정책이다.
- `zones`는 추가 보관 디렉터리 이름 목록이다. 소문자 영문으로 시작하고 영문·숫자·`_`·`-`만
  허용한다. workflow 별칭, kind·storage·예약 이름은 재선언할 수 없다.
- `zone-status`는 선언한 보관 zone에만 선택적으로 지정한다. 값은 pending, in-progress,
  review, blocked, done, cancelled 중 하나다. done이어도 실행 가능한 완료 zone이 되지 않는다.
- `transitions`가 없거나 `[]`이면 기존 기본 graph다. 비어 있지 않으면 **전체 graph를 대체**한다.
  위 예시에서는 두 전이만 허용하는 문서가 된다. 기본 graph에 두 행을 추가하는 뜻이 아니다.
- source는 canonical workflow 또는 선언한 보관 zone, target은 canonical workflow만 가능하다.
  보관 zone으로 새 write, kind/terminal 이동, 자기 자신으로 이동은 선언할 수 없다.
- 기본 graph: todo→doing; doing→todo/review/blocked; blocked→todo/doing;
  review→doing/done; done→todo. 보관 zone 선언만으로 전이를 추가하지 않는다.
- 입력은 UTF-8 단일 YAML 문서(동일 구조 JSON도 가능), 최대 64 KiB의 일반 파일이다.
  symlink·중복 키·알 수 없는 필드·null·잘못된 타입·anchor/alias/merge·custom tag는 오류다.
  카드 규칙의 `card-dialect` 파일이나 CE runtime 설정을 그대로 받는 명령이 아니다.
- 중첩 깊이는 16을 넘을 수 없고 정규화 결과도 64 KiB 이하여야 한다. 원본이 작아도
  정규화 후 상한을 넘는 문서는 거부한다. 수락한 문서의 정규화 결과를 다시 검증할 수 있다.

## 출력과 digest

성공 JSON은 `schemaVersion:1`, `scope:"policy-document"`, `valid:true`, `activated:false`,
`boardValidation:"not_evaluated"`, `canonical`, `digest`를 포함한다.
`canonical`은 같은 정책 schema의 compact JSON 객체다. zone·전이 source·target을 정렬하고
생략된 기본 graph를 명시한다. `digest`는 이 compact JSON bytes의 SHA-256 소문자 hex다.
JSON 바깥의 출력 newline이나 다른 출력 필드는 digest에 포함하지 않는다.
주석·공백·선언 순서가 달라도 의미가 같으면 digest가 같다. 상태나 허용 전이가 다르면 달라진다.
digest는 서명·실행 권한·정책 활성화 증거가 아니다.

성공은 exit 0, 파일·형식·의미 오류는 exit 1과 stderr 진단(성공 JSON 없음), 사용 오류는
exit 2다. `--help`는 exit 0이다. 원본·카드·예약·claim·journal을 변경하지 않으며 설정 안의
문자열을 실행하지 않는다. 파일을 보드에 복사하는 것으로 정책이 활성화되지는 않는다.

## 저장된 바인딩과 복구 호환

런타임은 journal v1의 기본 정책과 v2의 명시적 policy digest 바인딩을 구분한다.
v2에서는 `.task-manager-policy.json`의 정확한 canonical bytes가 journal의 digest와
일치해야 한다. 정책 파일만 있거나, 바인딩된 파일이 없거나 바뀌면 기본값으로 돌아가지
않고 보드 작업을 거부한다. 파일과 journal을 삭제해 우회하지 않는다.

v1에서 이어받은 완료 영수증은 기존 기본 정책으로 검증한다. 새 전이의 pending 및
completed 기록은 바인딩 digest를 유지한다. 중단 복구도 원래 정책이 필요하며, 변경된
정책으로 이동을 재해석하지 않는다. 선언된 보관 zone은 조회와 허용된 이동의 출발점만
지원한다. 그 안의 done 표기는 의존성을 완료시키지 않는다.

정책 활성화·업그레이드 명령은 아직 제공하지 않는다. 내부 파일을 직접 만들어 활성화하지
말고 기존 보드는 기본 정책으로 사용한다. 다중 worktree 정책 활성화와 공통 writer 장벽은
아직 지원하지 않으며, shared-ID 활성화는 정책 활성화를 의미하지 않는다.
