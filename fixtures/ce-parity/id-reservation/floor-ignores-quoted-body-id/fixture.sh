# fixture.sh: ID 원장 바닥이 본문에 인용된 TASK-999가 아니라 frontmatter·원장에서 온다 (ADR-0050)
PARITY_CMD=(new task --title "Floor probe" --criterion 'bound | verify: `test -f absent-floor-probe-marker.txt`')
PARITY_SNAP_GIT_CE=1
PARITY_SETUP='mkdir -p .git/ce/card-id-reservations && printf "{\"TASK\": 5}\n" > .git/ce/card-id-reservations/highest.json'
