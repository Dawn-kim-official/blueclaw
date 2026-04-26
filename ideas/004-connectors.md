# Blueclaw Connectors

## Direction

- connector runtime은 platform-neutral core와 얇은 platform adapter로 나눈다
- core는 수신, 중복 억제, 신원 확인, task 생성, progress, LLM reply, fallback reply, 전송, 로그를 한 번만 구현한다
- adapter는 normalized payload parsing과 InternKim capability 호출만 담당한다
- 공통 입력은 `PlatformInboundEvent`로 정규화한다
- 공통 reply 목적지는 `ReplyTarget`으로 표현한다
- 로그 이벤트명은 `connector.<platform>.<stage>` 형식을 사용한다

## Platform Baselines

- `Mattermost`는 `InternKim` 내부의 자체 호스팅 서비스다
- `Mattermost`는 `Blueclaw` guest 내부가 아니라 `InternKim` 내부의 별도 서비스다
- Mattermost WebSocket `posted` event 수신은 InternKim Mattermost sidecar가 담당한다
- Slack Events API 또는 Socket Mode 수신은 InternKim Slack sidecar가 담당한다
- Signal daemon/session credential은 InternKim Signal sidecar가 담당한다
- Blueclaw는 `/connectors/{platform}/events`에서 normalized `PlatformInboundEvent`만 받는다
- platform identity lookup, progress, reply, history fetch는 InternKim capability endpoint로 위임한다
- Blueclaw는 platform token, signing secret, WebSocket credential, Signal session secret을 읽지 않는다

## Conversation Rules

- InternKim은 platform topology를 `replyTargetID`와 `historyCursor`로 정규화한다
- Blueclaw는 thread, DM, channel root, linear group 차이를 추론하지 않는다
- inbound body는 `conversationID`, `messageID`, `senderID`, `replyTargetID`, `prompt`, `context`만 요구한다
- `context.messages`는 현재 prompt를 제외하고 오래된 순서에서 최신 순서로 제공한다
- `context.hasMoreBefore=true`이면 `historyCursor`가 반드시 있어야 한다

## Identity And Authorization

- platform user는 email 기반으로 Blueclaw person에 연결한다
- invited email이 아니면 task를 만들지 않고 가능한 경우 짧은 rejection reply를 보낸다
- platform account link는 connector core가 기억한다
- Blueclaw policy는 platform 권한을 넓히지 않고, platform access 이후 더 좁히는 역할을 한다

## Duplicate And Recovery Direction

- 같은 platform message는 같은 normalized message ID와 dedupe key를 가져야 한다
- Slack message ID는 `client_msg_id`, `event_ts`, `ts`, `event_id` 순서로 선택한다
- Slack/Mattermost/Signal reply destination은 Blueclaw에 platform-specific root/thread 값으로 노출하지 않고 `replyTargetID`에 담는다
- 현재 main은 in-memory duplicate suppression을 사용한다
- shared Postgres distributed ownership이 들어오면 같은 dedupe key를 DB-backed idempotency와 outbound claim에 연결한다

## Migration Goal

- `Slack` 사용 고객을 위해 `Slack export -> Mattermost import` 마이그레이션 경로를 지원 대상으로 둔다
- `Blueclaw`는 장기적으로 export 검증, 변환, import 진행 상태 모니터링을 오케스트레이션할 수 있어야 한다
- 단 `Slack` export 범위는 고객의 Slack 플랜과 승인 상태에 따라 달라질 수 있다
