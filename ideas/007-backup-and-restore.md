# Blueclaw Backup and Restore

- pg_dump 기반 snapshot backup bundle을 사용한다
- policy.yaml, runtime.yaml, secret.enc.yaml, workspace/skills, blob store, key envelope를 함께 보관한다
- 새 기기에서 restore 명령으로 bundle을 복원할 수 있어야 한다
- `workspace`는 guest에 연결되는 유일한 writable persistent volume로 사용한다
- DB 파일, blob store, backup bundle, policy/runtime/secret 파일은 `workspace/.blueclaw` 아래에 둔다
- guest image는 재생성 가능해야 하고 복구 대상은 image가 아니라 `workspace` 데이터다
