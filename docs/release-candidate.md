# 릴리스 후보와 출처

이 문서는 공개 배포 전 후보를 다시 만들고 확인하는 절차를 기록합니다. 현재
TaskChain Task Manager에는 공개 릴리스 태그, 배포 가능한 바이너리, 서명, 설치
자동화가 없습니다. 따라서 아래 후보의 checksum은 다운로드 검증값이 아니라 같은
소스와 도구체인에서 만든 로컬 결과를 비교하는 값입니다.

## 현재 선택한 후보

후보에는 버전 번호를 붙이지 않았습니다. 저장소에 기존 tag나 버전 정책이 없으므로
근거 없는 `v0.x` 또는 prerelease 번호를 새 계약처럼 제시하지 않기 위해서입니다.
후보의 식별자는 source commit입니다.

| 항목 | 값 |
| --- | --- |
| 상태 | 내부 검증 완료, 공개하지 않음 |
| 후보 버전 | 없음 (source commit으로 식별하는 무번호 후보) |
| 소스 저장소 | `github.com/Gizzahub/taskchain-task-manager` |
| source commit | `9e8fac7235a28cbab085a871e1475b38c5150417` |
| source tree | `766f5cbd9e3f6053e8a643ec2d01637aff06631f` |
| Go toolchain | `go1.27.0` |
| 플랫폼 | `darwin/arm64`, `GOARM64=v8.0`, `CGO_ENABLED=1`, `GOWORK=off` |
| 파일명 | `taskchain-task-manager-candidate` |
| SHA-256 | `9bc844af42714d0fa112acf0f05fac860d449026324f254db819d78685343fac` |
| 크기 | 7,785,410 bytes |

이 플랫폼에서 `make check`는 통과했습니다. 후보 checksum은 같은 task worktree와
별도 `git archive` 소스 디렉터리에서 각각 다시 빌드해 일치시켰습니다. 이 기록은 다른 플랫폼의
지원이나 checksum 일치를 약속하지 않습니다. 현재 공개적으로 확인된 후보 플랫폼은
표의 `darwin/arm64` 하나뿐입니다.

## 후보 재현

빈 작업 디렉터리에서 공개 저장소를 checkout하고, 표와 같은 Go toolchain 및 플랫폼인지
확인합니다. 아래 명령은 소스 파일을 바꾸지 않습니다.

```sh
set -eu
git clone https://github.com/Gizzahub/taskchain-task-manager.git
cd taskchain-task-manager
git checkout --detach 9e8fac7235a28cbab085a871e1475b38c5150417
test -z "$(git status --porcelain)"
test "$(go version)" = 'go version go1.27.0 darwin/arm64'
test "$(go env GOOS)" = darwin
test "$(go env GOARCH)" = arm64
test "$(go env GOARM64)" = v8.0
test "$(go env CGO_ENABLED)" = 1
test "$(GOWORK=off go env GOWORK)" = off

mkdir -p build
GOOS=darwin GOARCH=arm64 GOARM64=v8.0 CGO_ENABLED=1 GOWORK=off \
  go build -trimpath -buildvcs=false -mod=readonly \
  -o build/taskchain-task-manager-candidate ./cmd/taskchain-task-manager
shasum -a 256 build/taskchain-task-manager-candidate
```

마지막 출력은 다음과 같아야 합니다.

```text
9bc844af42714d0fa112acf0f05fac860d449026324f254db819d78685343fac  build/taskchain-task-manager-candidate
```

Go 버전, `CGO_ENABLED`, `GOARM64`, 운영체제 또는 CPU 아키텍처가 다르면 결과 checksum이 달라질 수
있습니다. 그 결과를 이 후보로 표시하거나 배포하면 안 됩니다. 후보 검사의 표준 빌드와
checksum은 플래그가 달라 같은 파일이 아닙니다.

```sh
make check
```

`make check`는 format, vet, race test, build를 실행합니다. 일부 race test는 오래 걸릴 수
있습니다. 이 후보에서 확인한 `make check`의 일반 build SHA-256은
`9e1ab401dc591c3752ef3638ef9b9055f7398328909706868bc27dba6c31ed11`입니다.

## CLI와 workspace Skill 예시

후보 파일과 Skill bundle을 새 임시 디렉터리에 복사해 `PATH` 앞에 둔 뒤,
포함된 합성 fixture로 workspace Skill의 두 조회 계약을 확인할 수 있습니다.
실제 보드와 기존 설치된 Skill bundle을 변경하지 않습니다.

```sh
set -eu
skill_tmp=$(mktemp -d "$PWD/build/skill-check.XXXXXX")
cp build/taskchain-task-manager-candidate "$skill_tmp/taskchain-task-manager"
cp -R skills/task-manager-workspace "$skill_tmp/task-manager-workspace"
export PATH="$skill_tmp:$PATH"
export TASK_MANAGER_SKILL_DIR="$skill_tmp/task-manager-workspace"
export TASK_MANAGER_FIXTURE_DIR="$skill_tmp/fixture"
test "$(command -v taskchain-task-manager)" = "$skill_tmp/taskchain-task-manager"

taskchain-task-manager --help
```

그 다음 [workspace Skill 합성 fixture](../skills/task-manager-workspace/assets/workspace/README.md)의
명령을 그대로 실행합니다. 해당 예시는
`workspace-context`와 `query-workspace`의 JSON 출력이 제공된 기대값과 일치하는지
`cmp`로 확인합니다. Skill은 `taskchain-task-manager`와 `git`이 `PATH`에 있어야 하며,
caller가 제공한 명시적 workspace manifest만 읽습니다.

## 공개 릴리스 전 절차

공개 배포는 별도의 승인된 작업에서 다음 순서로 수행합니다.

1. 배포할 source commit을 고정하고, release version과 지원 플랫폼을 명시합니다.
2. 각 플랫폼별 clean checkout에서 toolchain, `GOOS`, `GOARCH`, `GOARM64`, `CGO_ENABLED`, 정확한
   build 명령, 산출물 파일명·크기·SHA-256을 기록합니다.
3. 각 산출물의 source commit에서 `make check`와 필요한 CLI/Skill 호환 예시를 실행해
   결과를 기록합니다.
4. tag, 배포 위치, checksum 파일, 서명 방식, 설치 문서를 같은 공개 릴리스 단위로
   검토하고 게시합니다.

이 후보는 위 절차의 입력일 뿐입니다. 아직 tag push, artifact 게시, 서명, 소비자 pin은
이루어지지 않았습니다.
