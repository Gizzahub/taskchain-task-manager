# 릴리스 후보 절차

이 문서는 공개 배포 전 후보를 반복해서 만들고 확인하는 절차와 계약을 정의합니다.
후보는 공개 릴리스가 아닙니다. tag, 배포 위치, 서명, 다운로드 URL을 제공하지
않으므로 아래 checksum은 내려받기 검증값이 아니라 같은 소스와 도구체인에서 다시
만든 로컬 결과를 비교하는 값입니다. 첫 내부 후보 하나를 고정한 기록은
[릴리스 후보와 출처](release-candidate.md)에 남아 있습니다.

## 후보 버전 결정

후보에는 버전 번호를 만들지 않습니다. 저장소에 기존 tag나 버전 정책이 없는 상태에서
임의의 `v0.x`나 prerelease 번호를 제시하면 근거 없는 번호가 새 계약처럼 굳습니다.
후보의 식별자는 source commit이고, 출처 manifest가 commit과 tree 해시를 기록합니다.
버전 번호를 붙이는 일은 공개 릴리스 단계에서 source commit과 함께 승인될 때 처음
일어납니다.

## 후보 만들기

```sh
make release-candidate
```

작업 트리가 깨끗할 때만 실행합니다. 출처가 source commit과 1:1로 묶여야 하는데
커밋되지 않은 변경이 있으면 그 보증이 성립하지 않기 때문입니다. 실행하면
`build/release/` 아래에 다음이 만들어집니다.

| 파일 | 내용 |
| --- | --- |
| `taskchain-task-manager-<os>-<arch>` | 플랫폼별 바이너리 |
| `SHA256SUMS` | 바이너리별 `<SHA-256>  <파일명>` 목록 (이름순) |
| `provenance.json` | 출처 manifest (JSON) |

빌드는 모든 플랫폼에서 `go build -trimpath -buildvcs=false -mod=readonly`를
사용합니다. `-trimpath`와 `-buildvcs=false`는 절대 경로와 VCS 스탬프를 없애 같은
소스·도구체인에서 바이트가 같은 결과를 보장합니다. 이 문서의 검증은 이 결정론에
의존합니다.

`provenance.json`은 source revision(commit, tree), Go toolchain 버전, 플랫폼별
`GOOS`·`GOARCH`·`GOARM64`·`CGO_ENABLED`·`GOWORK`, 정확한 빌드 명령, 아티팩트별
SHA-256과 bytes를 기록합니다. 재현 검증이 manifest를 다시 만들어 비교할 수 있도록
빌드 시각이나 절대 경로 같은 비결정적 값은 넣지 않았습니다.

## 후보 검증

```sh
make release-candidate-verify
```

파일 존재만 확인하지 않습니다. 검증은 세 단계로, 아래 경우 반드시 실패합니다.

1. 누락 검사 — manifest, checksum, 플랫폼 바이너리가 모두 있는지 확인합니다.
2. 플랫폼 필드 검사 — 각 바이너리의 빌드 메타데이터(`go version -m`)가 매트릭스의
   `GOOS`·`GOARCH`·`GOARM64`·`CGO_ENABLED`와 일치하는지 확인합니다.
3. 재빌드-비교 — 전체 아티팩트를 임시 디렉터리에 다시 만들어 저장본과 파일 단위로
   바이트 비교합니다. 다시 만든 manifest는 현재 `git HEAD`를 새로 기록하므로,
   저장된 후보가 다른 commit에서 만들어졌다면 이 단계에서 실패합니다.

| 불일치 | 실패 지점 |
| --- | --- |
| 파일 누락 | 1단계 |
| 플랫폼 필드 불일치 | 2단계 |
| SHA-256 불일치 (아티팩트 변조) | 3단계 |
| source revision 불일치 | 3단계 (manifest diff) |

검증은 후보를 만든 것과 같은 commit, 같은 toolchain에서 실행해야 합니다. Go
버전이 다르면 바이너리 자체가 달라지므로 같은 이유로 실패합니다.

## 검증된 플랫폼 매트릭스

이번 실행에서 재빌드-비교가 통과한 플랫폼만 아래에 있습니다.

| 플랫폼 | 빌드 프로필 |
| --- | --- |
| `darwin/arm64` | `CGO_ENABLED=1`, `GOARM64=v8.0`, `GOWORK=off` |
| `linux/amd64` | `CGO_ENABLED=0`, `GOWORK=off` (크로스컴파일) |
| `linux/arm64` | `CGO_ENABLED=0`, `GOARM64=v8.0`, `GOWORK=off` (크로스컴파일) |

darwin/arm64는 첫 내부 후보가 기록한 프로필 그대로입니다. linux/arm64의
`GOARM64=v8.0`은 도구체인 기본값이 바뀌어도 결과가 흔들리지 않도록 명시한
값입니다. 세 플랫폼 모두 이번 실행에서 재빌드-비교로 바이트가 같음을 확인했고,
검증은 `go1.27.0` darwin/arm64 호스트에서 수행했습니다.

경계도 함께 기록합니다. linux 아티팩트는 크로스컴파일 결과로 바이트 재현만
확인했고, 대상 플랫폼에서 직접 실행한 검증은 이번 실행에 포함되지 않았습니다.
darwin/arm64에서의 `make check` 통과와 CLI·workspace Skill 동작은
[릴리스 후보와 출처](release-candidate.md)의 기록을 따릅니다. 이 매트릭스는
`Makefile`의 `RELEASE_MATRIX`가 정의하며, 검증이 통과한 플랫폼만 여기에
남깁니다.

## 후보 아티팩트 사용

설치 자동화는 제공하지 않습니다. 해당 플랫폼 바이너리를 원하는 위치에
`taskchain-task-manager` 이름으로 복사해 `PATH`에 두면 됩니다.

```sh
make release-candidate
make release-candidate-verify
install build/release/taskchain-task-manager-darwin-arm64 ~/bin/taskchain-task-manager
```

복사 경로는 예시입니다. `PATH`에 있는 디렉터리를 직접 선택하세요.

후보 바이너리는 이 저장소의 CLI와 같은 프로그램이므로 README의 workspace Skill
예제가 그대로 동작합니다.

## CI 범위 결정

이 절차가 추가될 때 `.github/workflows/check.yml`은 바꾸지 않았습니다. 후보
검증의 본뜻은 기록된 소스·도구체인에서 로컬로 재현하는 것이고, 매트릭스의
darwin/arm64 `CGO_ENABLED=1` 빌드는 ubuntu 러너가 제공하지 않습니다. CI에서
릴리스 아티팩트를 만들고 게시하는 커버리지는 공개 배포 단계의 승인된 결정으로
미뤄두고, `make check`는 지금처럼 format·vet·race test·build의 개발 게이트로
유지합니다.

## 첫 후보 기록과의 관계

[릴리스 후보와 출처](release-candidate.md)는 첫 내부 후보(commit `9e8fac7`)의
고정된 기록이고, 이 문서는 그 위에 세운 반복 가능한 절차와 계약입니다. 두 문서가
같은 checksum을 기대하지는 않습니다. checksum은 source revision에 묶이므로 커밋이
달라지면 달라집니다. 다만 첫 후보 이후의 커밋이 문서와 빌드 스크립트만 바꿨기
때문에, 이 절차가 추가된 시점의 darwin/arm64 아티팩트는 첫 후보와 같은
`9bc844af42714d0fa112acf0f05fac860d449026324f254db819d78685343fac` 바이트를
만듭니다. Go 소스를 바꾸는 커밋이 들어오면 이 일치는 더 이상 성립하지 않으며,
그때의 정확한 값은 `provenance.json`과 `make release-candidate-verify`가
결정합니다.

공개 배포는 별도의 승인된 작업이며, 그 단계는 [릴리스 후보와
출처](release-candidate.md)의 「공개 릴리스 전 절차」를 따릅니다.
