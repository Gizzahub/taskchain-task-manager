# fixture.sh: rc=$? 캡처와 out=$(...) || exit 1 전파는 인정되고, 잘린 캡처는 지적된다 (ISSUE-077)
PARITY_CMD=(validate --all)
