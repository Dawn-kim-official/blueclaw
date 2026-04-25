# Blueclaw Connectors

## Direction

- connector runtime은 platform-neutral core와 얇은 platform adapter로 나눈다
- core는 수신, 중복 억제, self-message 억제, 신원 확인, task 생성, typing or presence, LLM reply, fallback reply, 전송, 로그를 한 번만 구현한다
- adapter는 payload parsing, platform identity lookup, conversation kind 판단, typing or presence 전송, reply API 호출만 담당한다
- 공통 입력은 `PlatformInboundEvent`로 정규화한다
- 공통 reply 목적지는 `ReplyTarget`으로 표현한다
- 로그 이벤트명은 `connector.<platform>.<stage>` 형식을 사용한다

## Platform Baselines

- `Mattermost`는 `InternKim` 내부의 자체 호스팅 서비스다
- `Mattermost`는 `Blueclaw` guest 내부가 아니라 `InternKim` 내부의 별도 서비스다
- Mattermost ingress는 WebSocket `posted` event를 기본 realtime path로 사용한다
- Mattermost HTTP `/connectors/mattermost/events`는 테스트와 webhook-compatible path로 유지한다
- Mattermost outbound는 HTTP API로 post, file, typing indicator를 보낸다
- `Slack` v1 production ingress는 Events API HTTP callback이다
- Slack callback route는 `/connectors/slack/events`다
- Slack reply는 Slack Web API를 사용한다
- Slack request signature는 `signingSecretPath`가 있으면 검증한다
- Slack Socket Mode는 v1 baseline이 아니며, 나중에 `ConnectorTransport` 구현만 추가하는 방향으로 둔다
- `Signal`은 direct/group topology scaffold만 두고 production enable은 v1에서 거부한다

## Conversation Rules

- DM 또는 direct topology는 mention 여부와 무관하게 답변 대상이다
- DM reply는 parent 없이 direct reply로 보낸다
- threaded channel root message는 같은 채널 최근 context를 보고 새 subthread로 답한다
- existing thread message는 같은 thread context를 보고 same-context reply로 답한다
- linear group chat은 conversation context에 그대로 답한다
- platform-specific thread ID, root ID, timestamp 차이는 adapter 내부 transport detail로 둔다

## Identity And Authorization

- platform user는 email 기반으로 Blueclaw person에 연결한다
- invited email이 아니면 task를 만들지 않고 가능한 경우 짧은 rejection reply를 보낸다
- platform account link는 connector core가 기억한다
- Blueclaw policy는 platform 권한을 넓히지 않고, platform access 이후 더 좁히는 역할을 한다

## Duplicate And Recovery Direction

- 같은 platform message는 같은 normalized message ID와 dedupe key를 가져야 한다
- Slack message ID는 `client_msg_id`, `event_ts`, `ts`, `event_id` 순서로 선택한다
- Slack thread reply parent는 dedupe ID가 아니라 Slack `ts`를 사용한다
- Mattermost message ID와 reply root는 `post_id`와 `root_id`를 사용한다
- 현재 main은 in-memory duplicate suppression을 사용한다
- shared Postgres distributed ownership이 들어오면 같은 dedupe key를 DB-backed idempotency와 outbound claim에 연결한다

## Migration Goal

- `Slack` 사용 고객을 위해 `Slack export -> Mattermost import` 마이그레이션 경로를 지원 대상으로 둔다
- `Blueclaw`는 장기적으로 export 검증, 변환, import 진행 상태 모니터링을 오케스트레이션할 수 있어야 한다
- 단 `Slack` export 범위는 고객의 Slack 플랜과 승인 상태에 따라 달라질 수 있다
