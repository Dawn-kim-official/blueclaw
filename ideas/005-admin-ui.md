# Blueclaw Admin UI

- headless 하드웨어를 위한 원격 관리자 UI
- policy.yaml 편집, 검증, 감사 로그, 전체 작업 모니터 제공
- 저장은 백업과 hot reload를 동반한다
- 운영자는 사용자 메인컴퓨터에서 `SSH` 포트 포워딩 또는 `HTTP API`를 통해 접근한다
- 원격 웹 접근은 `Cloudflare Tunnel + Google OAuth` 뒤에서만 허용한다
- 원격 웹 접근 후에도 최초 설정한 관리자 계정 또는 `master admin` 계정만 통과시킨다
- 물리 디스플레이가 없는 환경을 전제로 설계한다
- 공개 인터넷에 직접 노출되는 관리 경로는 최소화한다
