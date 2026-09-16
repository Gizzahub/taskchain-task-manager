# 유지형 Intent와 Iteration 사용 계약

유지형 Intent는 지속적으로 관찰할 목표를 선언합니다. Iteration은 한 번의 관측과
판단을 저장합니다. Task Manager는 이 기록을 실행하거나 다음 작업을 계획하지 않습니다.
TASK 완료나 성공 기준의 `met`만으로 유지 목표가 영구 달성되었다고 판단하지 않습니다.

## 유지형 Intent

기존 completion Intent는 schemaVersion 1 그대로입니다. 유지형은 schemaVersion 2,
`mode: "maintenance"`이며 기존 Intent 필드에 필수 `maintenance`를 추가합니다.
v1 문서에 maintenance를 넣거나 v2에서 생략하면 오류입니다.

```json
{
  "triggers": [{"key": "manual-check", "kind": "manual"}],
  "budget": {
    "maxIterations": 10,
    "maxTasks": 20,
    "maxElapsedSeconds": 3600,
    "noProgressLimit": 3
  }
}
```

위 객체가 maintenance 값입니다. trigger는 1..128개이며 key는 성공 기준 key와 같은
문법이고 목록 내에서 고유합니다. kind는 manual/event/interval입니다. interval만
양의 `intervalSeconds`가 필수이고 다른 kind에는 이 필드가 없어야 합니다.
budget의 네 필드와 intervalSeconds는 모두 1..4294967295 정수입니다.
주기·이벤트는 선언일 뿐이며 자동 감시·예약 실행·권한 승인이 아닙니다.

## Iteration 필드

문서는 `schemaVersion: 2`, `kind: "iteration"`, `id: "ITERATION-…"`,
`revision: 1`, `intent`, `ordinal`, `trigger`, `usage`, `evaluation`이 필수입니다.
ID 접미사는 소문자 hex 32자입니다. 수정은 같은 ID의 revision을 올리지 않고 새 ID로
기록합니다. optional previous/batch는 없을 때 생략하며 null은 오류입니다.

- intent는 정확한 유지형 Intent의 id/revision/digest 참조입니다.
- ordinal은 root에서 1, previous가 있으면 이전 ordinal + 1입니다.
- previous는 이전 Iteration의 id/revision/digest, batch는 Batch의 같은 형태 참조입니다.
  digest는 `validate-context` 또는 등록 결과의 canonical SHA-256을 사용합니다.
- trigger는 선언된 key와 비어 있지 않은 evidenceRefs 문자열 배열입니다.
- usage는 tasks/elapsedSeconds/noProgressCount의 0..4294967295 정수입니다.
- evaluation은 actor, progress(bool), criteria, decision, stopReason, reason,
  remainingGaps, evidenceRefs를 포함합니다. 마지막 두 필드는 문자열 배열입니다.
- criteria는 Intent의 모든 successCriteria key를 정확히 한 번씩 포함합니다.
  각 항목은 key/result/evidenceRefs이며 result는 met/unmet/unknown입니다.
  met/unmet에는 하나 이상의 증거 참조가 필요합니다. unknown은 빈 배열을 허용합니다.

| decision | 허용 stopReason |
| --- | --- |
| continue, idle | none |
| blocked | permission-required, user-decision |
| revise | revision-change |
| stopped | budget-exhausted, no-progress, user-stop |

idle은 Batch가 없고 progress가 false여야 합니다. Iteration에는 achieved가 없습니다.
유지형 Intent에 연결한 v1 Batch 역시 achieved 평가를 등록할 수 없습니다.

## 계보·사용량·검증 경계

previous와 batch는 현재 Iteration과 같은 정확한 Intent revision/digest를 참조해야
합니다. stopped/revise 뒤에는 child를 붙일 수 없습니다. 새 Intent revision은 새 root로
시작하세요. fork와 여러 root는 허용하며 단일 head나 전역 실행 횟수를 보장하지 않습니다.

tasks는 계보 root 이후 생성한 Task의 누적 수, elapsedSeconds는 root 이후 경과 초에
대한 호출자 주장입니다. predecessor보다 줄어들 수 없지만 Task 수나 시계에서 자동
산출하지 않습니다. noProgressCount는 idle이면 이전 값(root는 0)을 유지하고,
그 외 progress true면 0, false면 이전 값 + 1(root는 1)입니다. overflow는 거부합니다.
예산 초과 관측도 저장할 수 있습니다. 예산 집행·실제 중단·새 root 남용 방지는 실행기
책임입니다. actor는 인증된 신원이 아니고 evidenceRefs의 내용은 실행·인증하지 않습니다.

```sh
taskchain-task-manager validate-context examples/context/iteration-idle.json --json
taskchain-task-manager register-context examples/context/maintenance-intent.json --dir ./tasks --json
# 예제 Iteration에는 위 예제 Intent의 정확한 digest가 들어 있습니다.
taskchain-task-manager register-context examples/context/iteration-idle.json --dir ./tasks --json
taskchain-task-manager show-context --dir ./tasks --kind iteration \
  --id ITERATION-0123456789abcdef0123456789abcdef --revision 1 --json
```

단일 파일 validate는 참조를 조회하지 않습니다. 신규 등록 시에만 관계를 검증합니다.
동일 기록 재등록과 역사 조회는 참조가 나중에 없어져도 not_rechecked로 반환할 수
있습니다. 기존 v1 canonical/digest는 바뀌지 않습니다. 구버전 CLI에 새 문서를 넘기지
마세요. 새 문서 거부는 기존 카드 작업 전체를 차단하는 board-wide version gate가 아닙니다.
