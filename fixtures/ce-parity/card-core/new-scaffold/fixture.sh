# fixture.sh: new가 스캐폴드 카드를 쓰고 카드 ID 원장을 예약한다 (.git/ce 스냅샷)
PARITY_CMD=(new task --title "Fixture scaffold card" --criterion 'bound | verify: `test -f absent-new-marker.txt`')
PARITY_SNAP_GIT_CE=1
