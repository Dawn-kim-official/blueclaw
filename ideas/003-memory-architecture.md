# Blueclaw Memory Architecture

- Graphiti가 canonical 장기기억이다
- Postgres는 Graphiti namespace, episode mirror, 진단 metadata만 저장한다
- legacy memory_record/content_segment 테이블은 forward migration으로 제거한다
- 검색은 정책 필터를 먼저 적용한 뒤 source kind, Graphiti score, recency로 결과를 구성한다
- raw episode는 durable fact처럼 프롬프트에 주입하지 않는다
