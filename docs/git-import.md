# Git 이력에서 ID 예약 가져오기

`import-ids`는 로컬 Git ref에서 도달 가능한 카드 이력을 읽어 삭제된 카드의 ID도
예약한다. 카드를 복사하거나 Git 상태를 변경하지 않는다.

```sh
# source-repo와 tasks는 예시다. 실제 저장소 루트와 저장소 상대 보드 경로를 지정한다.
./build/taskchain-task-manager import-ids --repo ./source-repo --board tasks --preview --json
./build/taskchain-task-manager import-ids --repo ./source-repo --board tasks --dir ./tasks --adopt --json
```

먼저 preview의 `history.refs`, `history.ids`와 수집 범위를 확인한다. 적용은 이력을 다시
읽으므로 preview와 같은 snapshot을 고정하는 승인 절차는 아니다. `--adopt`는 기존 보드에
ID 원장이 없을 때 필요하다. 이미 초기화된 대상에는 생략한다. 반복 적용은 합집합이며
기존 예약을 지우지 않는다. stdout 전송 실패 뒤에도 예약됐을 수 있어 같은 명령을 재시도한다.

## 수집 범위

- 로컬 branch, remote-tracking ref, tag, 기타 `refs/` 전체를 읽는다. fetch하지 않는다.
- merge 양쪽과 삭제 전 이력을 포함한다. ref가 가리키지 않는 detached HEAD, reflog,
  unreachable 객체, 미수신 remote 이력, 커밋되지 않은 삭제 이력은 포함하지 않는다.
- 파일명이 아니라 내용의 줄 시작 `id:`를 읽는다. TASK/PLAN/ISSUE/BACKLOG의 숫자
  identity를 정규화하며 본문·코드 블록의 후보도 보수적으로 예약한다.
- 보드 아래 Markdown 파일과 archive/_archive를 포함한다. README/INDEX/TEMPLATE 파일,
  보드 아래 `.ce`/`evidence` 경로는 제외한다. 후보 symlink는 오류다.
- 현재 보드 디렉터리가 삭제돼 있어도 과거 이력을 읽는다. `--board`는 glob이 아닌
  정규화된 저장소 상대 경로다. 저장소 전체를 뜻하는 `.`은 허용하지 않는다.

## 실패와 안전 경계

shallow 저장소, replace refs, grafts, partial/promisor 설정이 있는 저장소, 지원하지 않는
Git 환경 override, commit으로 해석할 수 없는 ref는 거부한다. promisor 설정은 `false`라도
존재하면 거부한다. 전체 로컬 이력을 가진 별도 저장소에서 검토 후 실행한다.
pager·서명 검증·fsmonitor·lazy fetch는 비활성화한다. 설치된 Git이 필요한 옵션을
지원하지 않으면 실패하며 네트워크 조회로 우회하지 않는다.

조회 제한은 2분, ref 4,096개, tree 8,192개, blob/ID 각각 65,536개다.
blob 하나는 16 MiB, Git 호출별 stdout과 전체 blob 응답은 64 MiB, stderr는 8 KiB로
제한한다. 초과·객체 누락·손상·조회 중 ref 변경이면 부분 결과를 대상에 적용하지 않는다.
성공한 조회만 보드 잠금 아래 기존 예약과 합친다.

마지막 ref 비교는 조회 snapshot 확인이지 Git 전체 잠금이 아니다. 조회 뒤 새 커밋이나
다른 clone의 예약까지 보호하지 않는다. 이관 중에는 원본 쓰기를 중단하고 필요한 ref를
사전에 확보한다. 이 기능만으로 CE 전체 dialect 호환이나 공유 worktree 예약이 완성되지는 않는다.
