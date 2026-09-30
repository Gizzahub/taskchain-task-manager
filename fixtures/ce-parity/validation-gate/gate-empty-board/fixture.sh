# fixture.sh: 카드가 하나도 없는 tasks 트리는 gate가 측정 불가(exit 2)로 응답한다
# 캡처 시점의 빈 tasks/ 디렉토리는 git이 보존하지 못하므로 PARITY_SETUP으로 복원한다
# (참조 바이너리 bb970b24도 이 입력에서 거절 경로로 FAIL — expected/는 변경하지 않는다)
PARITY_SETUP='mkdir -p tasks'
PARITY_CMD=(gate)
