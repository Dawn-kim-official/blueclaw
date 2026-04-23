# Blueclaw Security Model

- 접근 조건은 출처 접근권, 보안레벨, 보안클래스 집합을 모두 만족해야 한다
- 민감 정보 존재 자체를 드러내지 않는 거절 응답을 사용한다
- 본문과 파생 기억은 앱 레벨 암호화를 적용한다
- 정책은 파일 기반으로 관리하고 런타임은 읽기 전용 projection을 사용한다
- 관리자 경로는 `Cloudflare Tunnel` 뒤의 `Google OAuth`를 통과한 뒤에도 내부 역할 검사를 다시 통과해야 한다
- 관리자 UI 접근 허용 대상은 `bootstrap admin` 또는 `master admin`으로 제한한다
- 원격 인증과 내부 권한 검사는 별도 계층으로 유지한다
