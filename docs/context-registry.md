# Intent/Batch context registry

`register-context FILE --dir BOARD --json`은 strict JSON Intent/Batch 하나를
검증한 뒤 board-local registry에 불변 bytes로 게시합니다. 경로는
`.task-manager-context/intents/ID/revision.json` 또는
`.task-manager-context/batches/ID/revision.json`입니다. 희소 revision은 허용하지만
latest/head 원장이나 자동 증가를 만들지 않습니다.

Batch 신규 등록은 같은 잠금 안에서 정확한 Intent revision/digest와 보드의 TASK
identity를 확인합니다. Intent는 `referenceValidation: not_applicable`, 새 Batch는
`referenceValidation: verified`로 표시됩니다. 이미 등록된 Batch를 재시도하거나
조회할 때는 `not_rechecked`로 표시될 수 있으며 이후 TASK가 사라져도 역사 기록을
반환합니다. evaluation은 제출된 주장일 뿐 달성·권한을 인증하지 않습니다.

`show-context --dir BOARD --kind intent|batch --id ID --revision N --json`은
정확히 지정한 revision만 읽습니다. 정상 canonical bytes가 같으면 재등록 결과는
`unchanged`, 다른 bytes는 충돌이며 기존 파일을 덮어쓰지 않습니다. 경로 요소의
symlink·비정상 파일·손상 문서는 오류입니다.

등록 실패와 출력 실패 뒤에는 같은 board·kind·ID·revision·내용으로 재시도하세요.
게시 뒤 정리나 출력이 실패해도 문서가 이미 등록되었을 수 있습니다. 프로세스 중단
범위의 원자성만 보장하며 전원 손실, 외부 편집기, 서로 다른 clone/worktree 간
registry 공유는 보장하지 않습니다. 이 기능은 여러 Task 생성, 권한 승인,
자동 평가 실행을 하지 않습니다.
