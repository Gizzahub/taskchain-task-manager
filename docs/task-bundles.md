# Intent에 연결된 Task 일괄 생성

`create-bundle`은 사람이거나 상위 도구가 작성한 요청을 검증해 새 TASK들과 불변 Batch를
함께 게시한다. Intent를 LLM으로 분해하거나 작업을 실행·평가하는 명령은 아니다.

```sh
taskchain-task-manager init --dir ./tasks --json
taskchain-task-manager register-context examples/context/intent.json --dir ./tasks --json
taskchain-task-manager create-bundle examples/context/bundle.json --dir ./tasks --adopt --json
```

예제는 합성 데이터다. 실제 새 요청에는 새 32자리 lowercase hex `requestId`와 별도 Batch
ID/revision을 작성한다. 같은 보드에서 같은 requestId는 영구적으로 같은 canonical 요청에
묶인다. 같은 내용 재호출은 같은 key→ID 결과를 돌려주며 다른 내용은 오류다.

## 입력과 출력

- 입력과 canonical 요청은 각각 최대 256 KiB, TASK draft는 1–128개다.
- `batch`는 ID/revision, 정확한 등록 Intent의 ID/revision/digest, gap, constraints,
  authorizationRefs를 포함한다. 참조 문자열은 실제 권한 부여나 목표 달성 증거가 아니다.
- draft는 고유 key, id(빈 문자열이면 자동 할당), title, dependsOn을 가진다.
  참조는 `{ "taskId": "TASK-1", "key": "" }` 또는
  `{ "taskId": "", "key": "prepare" }` 중 하나다. taskId는 이미 존재하는 TASK,
  key는 같은 요청의 새 TASK다. 새 TASK를 taskId로 참조하지 않는다.
- 모든 명시 ID를 먼저 예약 후보로 잡고, 자동 ID는 요청 순서로 계산한다. 누락 의존성,
  중복·순환·사용된 ID·기존 Batch key·잘못된 카드 규칙은 게시 전에 거부한다.
- 선택적 `template`에는 단일 카드 생성과 같은 validationConfig 원문, type, priority,
  summary, criteria를 넣는다. 준비된 전체 카드와 Batch는 최대 1 MiB다.
- 성공 stdout은 schemaVersion, requestId, digest, status, replayed, tasks(key/id/path),
  batch를 가진 JSON 한 개다. `status: completed`는 **게시 트랜잭션 완료**일 뿐,
  생성한 TASK 완료·Intent 달성·검증 통과를 뜻하지 않는다.

## 최초 채택과 이전 바이너리

`--adopt`는 bundle journal 프로토콜 업그레이드의 명시적 동의다. ID 원장을 새로 만드는
옵션이 아니다. 먼저 보드의 모든 writer를 업그레이드하고 기존 파일을 백업한다.
로컬 transition journal에 bundleProtocol 표식과 별도 bundle journal이 추가된다.
공유 ID가 활성화된 보드는 common state도 schema 2로 바뀐다.

이전 strict reader는 새 표식/버전을 거부한다. common authority 자체를 모르는 과거
바이너리, 외부 편집기, 다른 clone을 소급 차단하지 않는다. 이전 버전으로 되돌리려고
표식·journal·예약을 삭제하면 안 된다. 업그레이드 후 원장 유실은 복원 대상으로 취급한다.

## 중단과 재개

```sh
taskchain-task-manager create-bundle request.json --dir ./tasks --resume --json
```

pending 기록이 있을 때만 같은 요청으로 `--resume`한다. journal 생성 전 실패했다면
원래 호출을 다시 실행한다. 빈 journal/프로토콜 채택 도중 실패했다면 `--adopt` 호출을
다시 실행한다. 완료된 요청의 재호출은 역사적 결과를 반환하며 삭제·이동된 카드를
다시 만들거나 현재 graph를 과거 결과로 덮어쓰지 않는다.

ID·카드 bytes·Batch·namespace·정책 binding은 최초 준비 결과에 고정된다. 재개는
ID를 재할당하지 않는다. 충돌 파일이나 다른 요청이 예약한 ID가 있으면 그대로 보존하고
실패한다. shared pending에는 실제 소유 보드 경로도 묶이므로 journal을 다른 worktree로
복사해 복구하지 않는다. 원래 위치와 정확한 파일을 복원한 뒤 재개한다.

게시 중 일반 board 명령은 pending 오류로 멈춘다. shared common pending은 다른
worktree에도 적용된다. 여러 파일의 단일 filesystem 원자적 변경은 아니므로 직접 파일을
읽는 프로그램에는 부분 게시가 보일 수 있다. 이 보장은 해당 CLI와 잠금을 지키는 writer에
한정되며 전원 손실·분산 clone·잠금을 무시하는 편집기를 포함하지 않는다.

프로세스 종료 후 lock이 남으면 살아 있는 프로세스가 없는지 먼저 확인한다. 도구는 자동
잠금 탈취·만료·rollback을 하지 않는다. 출력/정리 실패라도 게시가 끝났을 수 있으므로
새 requestId를 만들지 말고 동일 입력으로 결과를 확인한다.

journal은 8 MiB로 제한하고 완료 기록 공간을 사전 검사한다. 자동 이력 삭제는 없다.
일반 구문/저장 오류는 exit 1, 잘못된 CLI 인자는 exit 2다.
