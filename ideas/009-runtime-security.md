# Blueclaw Runtime Security

- ARM Linux를 기본 운영 환경으로 가정한다
- `Blueclaw` 자체를 `Firecracker` 격리 guest 안에서 실행하는 배포 형태를 기본으로 한다
- `InternKim`은 headless 장비이므로 관리 경로는 `SSH`, `API`, 제한된 원격 웹 경로로만 연다
- 제한된 원격 웹 경로는 `Cloudflare Tunnel + Google OAuth` 뒤의 관리자 경로로만 연다
- terminal execution은 사용자 권한 범위에서만 수행한다
- system modification 명령은 hard deny 한다
- shell inline eval은 거부한다
- glab을 GitLab CLI 표준으로 허용한다

## Practical Security Direction

- host는 `Blueclaw guest` 하나만 띄우고 host file system에서는 `workspace` 하나만 guest에 연결한다
- `Cloudflare Tunnel`은 원격 접근 편의 레이어일 뿐이고 privileged surface 확장 수단이 되어서는 안 된다
- `Cloudflare Tunnel` 관리 경로는 `bootstrap admin` 또는 `master admin` 계정으로 다시 제한한다
- `Mattermost`는 `Blueclaw`와 분리된 `InternKim` 내부 서비스로 유지한다
- guest rootfs는 immutable image로 유지한다
- 지속 데이터는 `workspace/.blueclaw` 아래에만 저장한다
- guest 내부 실행은 `Firecracker` guest 경계를 기본 보호막으로 사용한다
- guest 내부 명령은 host 경계가 아니라 guest 경계를 기준으로 제한한다
- 리소스 제한은 느슨하게 두되 host device, host rootfs, privileged socket 연결은 엄격히 금지한다

## Firecracker Direction

- `Firecracker`는 선택지가 아니라 기본 guest runtime으로 사용한다
- `Blueclaw 전체 guest`를 하나의 long-lived microVM으로 띄운다
- host에는 root 소유의 `sandbox-supervisor`를 두고 이 프로세스만 `jailer`, `/dev/kvm`, network namespace, cgroup을 다룬다
- host는 guest rootfs image와 `workspace` volume만 연결한다
- guest 내부의 `Blueclaw` 프로세스는 계속 비권한 사용자로 실행한다
- tool 실행은 guest 내부에서 직접 수행한다
- host와 guest 사이의 writable 공유는 `workspace` 하나만 허용한다
- host root filesystem, Docker socket, broad shared mount, privileged device는 guest에 절대 연결하지 않는다
- 리소스 상한은 host 보호를 위한 최소치만 두고, guest가 대부분의 CPU와 메모리를 사용하는 것은 허용한다
- `Jetson Orin Nano`와 `Jetson Orin Nano Super`에서는 실제 `KVM` 경로 검증을 거친 뒤 활성화한다
