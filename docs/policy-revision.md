# 명시적 보드 정책 변경

`activate-policy`는 최초 채택·동일 정책 join용이며 기존 정책을 덮어쓰지 않는다.
활성 정책을 변경할 때는 `revise-policy`에 **이전 authority ID와 digest**를 모두 전달한다.
모든 writer를 현재 버전으로 업그레이드하고 중지한 뒤, pending 작업을 복구하고 held claim을
정상적으로 해제한다. 같은 digest로의 변경과 자동 정책 변경은 지원하지 않는다.

```sh
# 이미 채택한 정책의 결과를 재확인해 authorityId와 digest를 얻는다.
taskchain-task-manager activate-policy current-policy.yaml --dir ./tasks --json
taskchain-task-manager validate-policy next-policy.yaml --json

# 아래 자리표시자를 위 결과의 값으로 바꾼다.
taskchain-task-manager revise-policy next-policy.yaml --dir ./tasks \
  --expected-authority PREVIOUS_AUTHORITY_ID \
  --expected-digest PREVIOUS_POLICY_SHA256 --json
```

공유 ID namespace에서는 `--all-worktrees`가 필수다. 모든 등록 worktree가 기존 shared
authority에 명시적으로 join된 상태여야 하며, 어느 하나라도 충돌하면 최초 변경을 거부한다.
공유 revision 진행 중에는 일반 명령과 새 join이 차단된다. Git branch/worktree를 만들거나
수정하지 않는다. 파일 시스템의 카드·ID·claim·기존 완료 기록은 변경하지 않는다.

새 정책은 새 authority ID를 갖는다. 기존 완료 transition은 당시 정책으로 검증하고,
새 작업은 새 정책에 따른다. 나중에 같은 정책으로 돌아가도 이전 authority를 재사용하지
않으므로 오래된 변경 요청을 다시 실행할 수 없다. v1에서 v2 relocation 정책으로의 변경도
이 명령으로 수행하며, 실제 카드 이동은 별도의 `relocate` 요청이다.

## 중단·재개·결과 확인

중단 후에는 **원래 target 정책과 이전 authority/digest 값을 그대로** 유지한다.
현재 일부 파일에 보이는 새 값으로 요청을 바꾸거나 journal·lock을 삭제하지 않는다.

```sh
taskchain-task-manager revise-policy next-policy.yaml --dir ./tasks \
  --expected-authority PREVIOUS_AUTHORITY_ID \
  --expected-digest PREVIOUS_POLICY_SHA256 --resume --json
# 공유 namespace에서는 위 명령에 --all-worktrees도 추가한다.
```

`--resume`은 저장된 요청만 재개하며 새 변경을 시작하지 않는다. pending 기록이 아직
없다면 원래 명령으로 재시도한다. 이미 완료됐다면 동일 요청은 읽기 전용 결과 확인이다.
그 뒤 다른 revision이 완료되면 오래된 요청은 거부된다. 공유 재개는 어느 참여 보드에서나
가능하지만 모든 참여 보드의 원래 HEAD·카드·원장과 파일 hash를 다시 확인한다.
충돌한 상태는 자동 수정하지 않는다. lock은 소유 프로세스 종료를 확인한 뒤 별도 처리한다.

성공 JSON은 활성화 명령과 같은 `schemaVersion:1`, `authorityId`, `scope`, `digest`,
`status`, `replayed`, `boards` 필드를 사용한다. 완료 재호출의 `boards`는 1이다.
exit 0은 성공, 1은 입력·상태·게시·출력 오류, 2는 CLI 사용 오류다. 진단은 stderr에 쓴다.
출력 실패가 작업 미실행을 뜻하지는 않는다. 동일 요청으로 결과를 확인한다.

## 저장 형식과 호환성

revision은 local activation schema2와 transition journal schema4를 사용한다.
공유 namespace에는 local 변경보다 먼저 영구 `policyRevisionProtocol:1`을 기록한다.
새 worktree join 후에도 차단 표식을 보존하며, 표식 유실은 복원이 필요한 오류다.
구버전과의 혼합 writer 운용이나 수동 파일 교체는 지원하지 않는다. 이 장벽이 임의의 외부
편집기까지 차단하는 것은 아니다. 카드 완료 검증·테스트·Intent 달성 판정과도 별개다.
