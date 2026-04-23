# Blueclaw Memory Architecture

- raw_event는 제한된 기간 보관한다
- memory_record는 장기기억으로 유지한다
- content_segment와 memory_record는 768 차원 임베딩을 저장한다
- 검색은 정책 필터를 먼저 적용한 뒤 결과를 구성한다
