# Blueclaw Product Outline

- 회사용 미니멀 AI 에이전트
- Slack과 Mattermost 실시간 수신
- DM, 멘션, 제한된 스레드 응답
- 사용자별 장기기억과 보안 정책 강제
- 관리자 UI와 사용자 작업함 제공
- `InternKim` 하드웨어 안에서 데몬으로 실행한다
- `InternKim`은 화면이 없는 headless 하드웨어다
- `InternKim`은 `Cloudflare Tunnel`로 어디서든 접속 가능해야 한다
- 관리자 원격 접근은 `Cloudflare Tunnel + Google OAuth`를 기본으로 한다
- 관리자 UI는 최초 설정한 관리자 계정 또는 `master admin` 계정만 접근 가능해야 한다
- `Mattermost`는 `InternKim` 내부의 별도 서비스로 자체 호스팅한다
- 기존 `Slack` 사용 고객은 가능한 경우 기존 데이터와 함께 `InternKim` 내부 `Mattermost`로 이전할 수 있어야 한다
- 사용자 메인컴퓨터와 통신할 수 있어야 한다
- 로그인이나 승인 흐름이 필요하면 사용자 메인컴퓨터에서 브라우저를 띄워 요청할 수 있어야 한다
- `Blueclaw` 설정과 운영은 사용자 메인컴퓨터에서 `SSH` 또는 `API`를 통해 수행한다
- 기본 배포 형태는 `Blueclaw 전체를 Firecracker guest 안에서 실행`하는 방식으로 간다
- host와 guest 사이의 공유 경로는 `workspace` 하나만 허용한다
- guest rootfs는 immutable image로 유지하고 모든 지속 데이터는 `workspace` 아래에 둔다
