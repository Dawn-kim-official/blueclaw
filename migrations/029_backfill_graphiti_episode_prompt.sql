UPDATE graphiti_episode episode
SET prompt = convert_from(raw_event.content_ciphertext, 'UTF8')
FROM raw_event
WHERE episode.prompt = ''
  AND raw_event.platform = episode.source_platform
  AND raw_event.external_message_id = episode.source_message_id
  AND length(raw_event.content_ciphertext) > 0;
