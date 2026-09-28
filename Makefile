.PHONY: build test lint check release-candidate release-candidate-verify

RELEASE_DIR ?= build/release
# provenance manifest의 buildCommand에 기록하는 공식 출력 경로다.
# release-candidate-verify가 RELEASE_DIR을 임시 디렉터리로 바꿔 재현해도
# manifest에는 공개 절차가 쓰는 경로를 기록한다.
RELEASE_RECORDED_DIR = build/release
# 매트릭스 한 줄: GOOS GOARCH GOARM64('-' 는 미지정) CGO_ENABLED
RELEASE_MATRIX = 'darwin arm64 v8.0 1' 'linux amd64 - 0' 'linux arm64 v8.0 0'

build:
	mkdir -p build
	GOWORK=off go build -mod=readonly -o build/taskchain-task-manager ./cmd/taskchain-task-manager

test:
	GOWORK=off go test -mod=readonly -race -timeout 40m ./...

lint:
	GOWORK=off go vet -mod=readonly ./...
	test -z "$$(gofmt -l cmd internal)"

check: lint test build

# 매트릭스 플랫폼별 바이너리, SHA256SUMS, provenance manifest를 RELEASE_DIR에 만든다.
# 출처가 source commit과 1:1로 묶여야 하므로 깨끗한 작업 트리에서만 실행한다.
release-candidate:
	@set -eu; \
	test -z "$$(git status --porcelain)" || { echo "release-candidate: 작업 트리가 깨끗하지 않아 source commit을 출처로 기록할 수 없습니다" >&2; exit 1; }; \
	mkdir -p build; \
	work=$$(mktemp -d "$$PWD/build/release-candidate.XXXXXX"); \
	trap 'rm -rf "$$work"' EXIT INT TERM; \
	rm -f "$(RELEASE_DIR)"/taskchain-task-manager-* "$(RELEASE_DIR)/SHA256SUMS" "$(RELEASE_DIR)/provenance.json"; \
	mkdir -p "$(RELEASE_DIR)"; \
	rev=$$(git rev-parse HEAD); \
	tree=$$(git rev-parse 'HEAD^{tree}'); \
	gover=$$(go version); \
	sha256() { if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$$@"; else sha256sum "$$@"; fi; }; \
	printf '%s\n' $(RELEASE_MATRIX) | while read -r goos goarch goarm64 cgo; do \
		name="taskchain-task-manager-$$goos-$$goarch"; \
		out="$(RELEASE_DIR)/$$name"; \
		if [ "$$goarm64" = "-" ]; then arm=''; else arm="GOARM64=$$goarm64"; fi; \
		echo "release-candidate: $$goos/$$goarch CGO_ENABLED=$$cgo → $$name"; \
		env GOWORK=off GOOS="$$goos" GOARCH="$$goarch" CGO_ENABLED="$$cgo" $$arm \
			go build -trimpath -buildvcs=false -mod=readonly -o "$$out" ./cmd/taskchain-task-manager; \
		sha=$$(sha256 "$$out" | cut -d' ' -f1); \
		size=$$(wc -c < "$$out" | tr -d ' '); \
		printf '%s  %s\n' "$$sha" "$$name" >> "$(RELEASE_DIR)/SHA256SUMS"; \
		printf '%s %s %s %s %s %s %s\n' "$$goos" "$$goarch" "$$goarm64" "$$cgo" "$$name" "$$sha" "$$size" >> "$$work/matrix.tsv"; \
	done; \
	{ \
		printf '{\n'; \
		printf '  "schemaVersion": 1,\n'; \
		printf '  "kind": "release-candidate",\n'; \
		printf '  "note": "internal release candidate, not a published release; no tag, no distribution, no signature",\n'; \
		printf '  "procedure": "docs/release.md",\n'; \
		printf '  "candidateVersion": "none",\n'; \
		printf '  "candidateIdentification": "source commit",\n'; \
		printf '  "repository": "github.com/Gizzahub/taskchain-task-manager",\n'; \
		printf '  "source": {\n'; \
		printf '    "commit": "%s",\n' "$$rev"; \
		printf '    "tree": "%s"\n' "$$tree"; \
		printf '  },\n'; \
		printf '  "toolchain": {\n'; \
		printf '    "goVersion": "%s"\n' "$$gover"; \
		printf '  },\n'; \
		printf '  "artifacts": [\n'; \
		first=''; \
		while read -r goos goarch goarm64 cgo name sha size; do \
			if [ "$$goarm64" = "-" ]; then goarm64=''; fi; \
			cmd="GOWORK=off GOOS=$$goos GOARCH=$$goarch"; \
			if [ -n "$$goarm64" ]; then cmd="$$cmd GOARM64=$$goarm64"; fi; \
			cmd="$$cmd CGO_ENABLED=$$cgo go build -trimpath -buildvcs=false -mod=readonly -o $(RELEASE_RECORDED_DIR)/$$name ./cmd/taskchain-task-manager"; \
			if [ -n "$$first" ]; then printf '    },\n'; fi; \
			printf '    {\n'; \
			printf '      "name": "%s",\n' "$$name"; \
			printf '      "goos": "%s",\n' "$$goos"; \
			printf '      "goarch": "%s",\n' "$$goarch"; \
			printf '      "goarm64": "%s",\n' "$$goarm64"; \
			printf '      "cgoEnabled": "%s",\n' "$$cgo"; \
			printf '      "gowork": "off",\n'; \
			printf '      "buildCommand": "%s",\n' "$$cmd"; \
			printf '      "sha256": "%s",\n' "$$sha"; \
			printf '      "bytes": %s\n' "$$size"; \
			first=1; \
		done < "$$work/matrix.tsv"; \
		printf '    }\n'; \
		printf '  ]\n'; \
		printf '}\n'; \
	} > "$(RELEASE_DIR)/provenance.json"; \
	echo "release-candidate: 완료 → $(RELEASE_DIR) (source commit $$rev)"

# 저장된 아티팩트 전부를 같은 소스·도구체인에서 다시 빌드해 바이트 단위로 비교한다.
# SHA-256 불일치, source revision 불일치, 플랫폼 필드 불일치, 파일 누락이면 실패한다.
release-candidate-verify:
	@set -eu; \
	dir='$(RELEASE_DIR)'; \
	for f in provenance.json SHA256SUMS; do \
		test -f "$$dir/$$f" || { echo "release-candidate-verify: $$dir/$$f 이(가) 없습니다. 먼저 make release-candidate 를 실행하세요" >&2; exit 1; }; \
	done; \
	for f in "$$dir"/taskchain-task-manager-*; do \
		test -f "$$f" || { echo "release-candidate-verify: 플랫폼 바이너리 $$f 이(가) 없습니다" >&2; exit 1; }; \
	done; \
	printf '%s\n' $(RELEASE_MATRIX) | while read -r goos goarch goarm64 cgo; do \
		name="taskchain-task-manager-$$goos-$$goarch"; \
		meta=$$(go version -m "$$dir/$$name"); \
		for field in "GOOS=$$goos" "GOARCH=$$goarch" "CGO_ENABLED=$$cgo"; do \
			printf '%s\n' "$$meta" | grep -q "$$field" || { echo "release-candidate-verify: $$name 플랫폼 필드 불일치 (기대 $$field)" >&2; exit 1; }; \
		done; \
		if [ "$$goarm64" = "-" ]; then \
			if printf '%s\n' "$$meta" | grep -q 'GOARM64='; then \
				echo "release-candidate-verify: $$name 은(는) GOARM64 필드가 없어야 합니다" >&2; exit 1; \
			fi; \
		else \
			printf '%s\n' "$$meta" | grep -q "GOARM64=$$goarm64" || { echo "release-candidate-verify: $$name 플랫폼 필드 불일치 (기대 GOARM64=$$goarm64)" >&2; exit 1; }; \
		fi; \
	done; \
	mkdir -p build; \
	work=$$(mktemp -d "$$PWD/build/release-verify.XXXXXX"); \
	trap 'rm -rf "$$work"' EXIT INT TERM; \
	$(MAKE) --no-print-directory RELEASE_DIR="$$work" release-candidate; \
	if diff -r "$$dir" "$$work"; then \
		echo "release-candidate-verify: 통과 — 아티팩트와 출처가 같은 소스·도구체인에서 재현되었습니다"; \
	else \
		echo "release-candidate-verify: 재현 빌드가 저장된 아티팩트와 다릅니다 (SHA-256, source revision, 플랫폼 필드 또는 파일 구성 불일치)" >&2; \
		exit 1; \
	fi
