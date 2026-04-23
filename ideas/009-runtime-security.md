# Blueclaw Runtime Security

- ARM Linux를 기본 운영 환경으로 가정한다
- 저램 환경을 우선한다
- terminal execution은 사용자 권한 범위에서만 수행한다
- system modification 명령은 hard deny 한다
- workspace root 밖의 working directory와 path operand는 거부한다
- shell inline eval은 거부한다
- Linux에서는 bubblewrap sandbox를 기본으로 사용한다
- sandbox를 사용할 수 없고 network 차단 요구가 있으면 실행 자체를 거부한다
- glab을 GitLab CLI 표준으로 허용한다

## Low-Memory Recommendation

- v1 기본 sandbox provider는 `bubblewrap`으로 고정한다
- `bubblewrap`은 프로세스 격리 계층이라 microVM 계열보다 메모리 부담이 작다
- `gVisor`는 다음 단계 후보로 둔다
- `Firecracker`와 `Kata Containers`는 강한 격리를 주지만 v1의 저램 기본값으로는 채택하지 않는다
- `E2B`와 `Daytona`는 원격 실행 오프로딩 옵션으로만 둔다

## Practical Security Direction

- 로컬 실행은 `bubblewrap + allowlist + denied path + denied inline eval + no root + workspace bound` 조합으로 간다
- 더 강한 격리가 필요하면 v2에서 `gVisor` 또는 `Kata/Firecracker` 백엔드를 추가한다
- 장기적으로는 민감 작업만 원격 sandbox로 보내는 hybrid 구성이 가장 현실적이다
