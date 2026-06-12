# Workspace Session Mode

- 사용자가 본인 워크스페이스 안에서는 `Claude Code`처럼 연속 작업을 시킬 수 있어야 한다
- 슬라이드 생성 → 수정 반복 → 웹사이트 생성 → 수정 반복 같은 다회차 개선 루프를 1급 흐름으로 취급한다
- 보안 경계는 기존 그대로 유지한다: Firecracker guest + POSIX 사용자/그룹 투영 + workspace 경로 경계
- 사람별 VM이나 컨테이너를 추가로 띄우지 않는다. `terminal.run`이 이미 요청자 UID로 본인 영역에서 실행되므로 "자유라는 환상"은 격리 추가가 아니라 세션 연속성으로 제공한다

## Session Continuity

- 같은 대화에서 이어지는 요청은 같은 작업 컨텍스트를 본다: 직전 작업 디렉터리, 직전 산출물 매니페스트, 직전 도구 결과 요약
- 산출물 매니페스트는 대화 스코프로 프롬프트에 주입한다 (path, 생성 도구, 마지막 수정 시각)
- 수정 요청은 새 산출물 생성이 아니라 기존 소스 수정 후 재빌드를 기본 동작으로 한다
- 사이트는 이미 draft workspace + `site.app.diff/history/restore` 로 이 모델을 따른다. 슬라이드와 문서도 같은 모델로 정렬한다: 소스는 `artifacts/<slug>/source`에 영속, 빌드 산출물만 갱신

## Boundary Rules

- 세션 모드의 파일 접근 범위는 요청자의 기존 POSIX 권한과 동일하다. 세션이라고 해서 grant가 넓어지지 않는다
- circle/shared 영역 작업은 해당 그룹 권한을 가진 요청자에게만 열린다
- `/workspace/.blueclaw/*` 서비스 영역과 denied executable 목록은 세션 모드에서도 동일하게 적용한다
- `bwrap` 같은 추가 격리는 v1 필수가 아니다. 이후 선택적 narrowing 후보로만 남긴다

## Failure And Recovery

- 런타임 재시작으로 끊긴 세션 작업은 중단 지점의 goal과 검증된 contract로 1회 자동 재개한다
- 재개 불가 시 LLM이 중단 사실과 이어서 하는 방법을 사용자 언어로 설명한다
