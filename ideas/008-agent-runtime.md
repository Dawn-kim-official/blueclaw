# Blueclaw Agent Runtime

- native Go 코어와 내부 subagent를 사용한다
- MCP client와 Agent Skills를 지원한다
- 장기 작업은 task state machine으로 관리한다
- ARM Linux에서는 `Firecracker` guest 안에서 장기 실행하는 운영 모드를 기본으로 한다
- 기본 운영 모드는 `Blueclaw 전체가 long-lived Firecracker guest 안에서 실행`되는 형태다
- agent가 접근 가능한 파일 시스템은 guest 내부 rootfs와 연결된 `workspace` 및 guest 내부 표준 경로다
- host file system은 `workspace` 외에 guest로 노출하지 않는다
- 메인컴퓨터 브리지를 통해 브라우저 실행과 사용자 로그인 위임을 요청할 수 있어야 한다
- 장기 작업은 `login required`, `approval required`, `resume after browser handoff` 상태를 가질 수 있어야 한다
- 관리 작업은 `InternKim`의 로컬 화면이 아니라 사용자 메인컴퓨터의 `SSH` 또는 `API` 세션을 통해 수행한다
- `Mattermost`는 `InternKim` 내부의 별도 서비스로 두고 runtime graph 안에서 `Blueclaw` guest 외부 노드로 다룬다
