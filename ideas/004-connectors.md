# Blueclaw Connectors

- Slack은 Socket Mode 기반으로 연결한다
- Mattermost는 WebSocket과 HTTP API 조합으로 연결한다
- 공통 입력 모델로 정규화한 뒤 보안 라벨과 작업 엔진으로 전달한다
- `Mattermost`는 외부 SaaS가 아니라 `InternKim` 내부의 자체 호스팅 서비스로 가정한다
- `Mattermost`는 `Blueclaw` guest 내부가 아니라 `InternKim` 내부의 별도 서비스다
- `Blueclaw`와 `Mattermost` 사이 연결은 우선 내부 네트워크 경계 안에서 해결한다
- 원격 관리용 `Cloudflare Tunnel`과 메시지 커넥터 트래픽은 분리한다
- `Slack` 사용 고객을 위해 `Slack export -> Mattermost import` 마이그레이션 경로를 지원 대상으로 둔다
- `Blueclaw`는 장기적으로 export 검증, 변환, import 진행 상태 모니터링을 오케스트레이션할 수 있어야 한다
- 단 `Slack` export 범위는 고객의 Slack 플랜과 승인 상태에 따라 달라질 수 있다
