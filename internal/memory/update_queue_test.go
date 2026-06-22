package memory

import "testing"

func TestMemoryEpisodeFromUpdateJobSetsUniqueMessageID(t *testing.T) {
	job := PrepareMemoryUpdateJob(MemoryUpdateJob{
		Namespace:      MemoryNamespace{NamespaceID: "user:person-1", ScopeType: ScopeTypeUser, ScopePersonID: "person-1"},
		Content:        "샘플 님은 면보다 밥을 더 좋아합니다.",
		Platform:       "mattermost",
		SenderPersonID: "person-1",
	})
	episode := memoryEpisodeFromUpdateJob(job)
	if episode.MessageID == "" {
		t.Fatal("expected a non-empty source message id so the graphiti_episode unique (source_platform, source_message_id) constraint is not violated by repeated remembers")
	}
	if episode.MessageID != episode.EpisodeID {
		t.Fatalf("expected message id to equal the unique job id, got message=%q episode=%q", episode.MessageID, episode.EpisodeID)
	}
}
