package adminapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"blueclaw/internal/identity"
	"blueclaw/internal/memory"
	"blueclaw/internal/policy"
)

type MemoryGraphHandler struct {
	MemoryService *memory.MemoryService
	Reporter      memory.GraphMemoryReporter
	Migrator      memory.GraphMemoryMigrator
	MarkdownStore *memory.MarkdownStore
	Identity      *identity.IdentityService
}

func (memoryGraphHandler MemoryGraphHandler) HandleGetMemoryGraph(responseWriter http.ResponseWriter, request *http.Request) {
	graph := memory.MemoryGraph{}
	if memoryGraphHandler.MemoryService != nil {
		graph.Health = memoryGraphHandler.MemoryService.Health(request.Context())
	}
	if memoryGraphHandler.Reporter == nil {
		writeJSON(responseWriter, http.StatusOK, graph)
		return
	}

	reportGraph, errorValue := memoryGraphHandler.Reporter.ListMemoryGraph(request.Context(), memoryGraphLimit(request))
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}
	reportGraph.Health = graph.Health
	reportGraph = memoryGraphHandler.filterGraphForReader(request, reportGraph)
	reportGraph = memoryGraphHandler.attachSearchFacts(request, reportGraph)
	reportGraph = memoryGraphHandler.attachPinnedFacts(request, reportGraph)
	reportGraph = rebuildMemoryGraphTopology(reportGraph)
	writeJSON(responseWriter, http.StatusOK, reportGraph)
}

func (memoryGraphHandler MemoryGraphHandler) HandleMigrateIdentity(responseWriter http.ResponseWriter, request *http.Request) {
	var body struct {
		Mappings []memory.MemoryIdentityMapping `json:"mappings"`
	}
	if errorValue := json.NewDecoder(request.Body).Decode(&body); errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusBadRequest)
		return
	}
	if len(body.Mappings) == 0 {
		http.Error(responseWriter, "mappings must not be empty", http.StatusBadRequest)
		return
	}
	for _, mapping := range body.Mappings {
		if strings.TrimSpace(mapping.OldPersonID) == "" || strings.TrimSpace(mapping.NewPersonID) == "" {
			http.Error(responseWriter, "oldPersonID and newPersonID must not be blank", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(mapping.OldPersonID) == strings.TrimSpace(mapping.NewPersonID) {
			http.Error(responseWriter, "oldPersonID and newPersonID must differ", http.StatusBadRequest)
			return
		}
	}

	if memoryGraphHandler.Migrator == nil {
		http.Error(responseWriter, "memory migrator is not configured", http.StatusServiceUnavailable)
		return
	}

	results, errorValue := memoryGraphHandler.Migrator.MigrateMemoryIdentities(request.Context(), body.Mappings)
	if errorValue != nil {
		http.Error(responseWriter, errorValue.Error(), http.StatusInternalServerError)
		return
	}

	for index, mapping := range body.Mappings {
		renamed, conflict, renameError := memoryGraphHandler.MarkdownStore.RenamePersonDirectory(mapping.OldPersonID, mapping.NewPersonID)
		if renameError != nil {
			http.Error(responseWriter, renameError.Error(), http.StatusInternalServerError)
			return
		}
		results[index].MarkdownRenamed = renamed
		results[index].MarkdownConflict = conflict
	}

	writeJSON(responseWriter, http.StatusOK, map[string]any{"results": results})
}

func (memoryGraphHandler MemoryGraphHandler) attachPinnedFacts(request *http.Request, graph memory.MemoryGraph) memory.MemoryGraph {
	if memoryGraphHandler.MarkdownStore == nil {
		return graph
	}
	readerPersonID := strings.TrimSpace(request.URL.Query().Get("readerPersonID"))
	if readerPersonID == "" {
		return graph
	}
	pinnedFacts, errorValue := memoryGraphHandler.MarkdownStore.LoadPinnedMemory(request.Context(), readerPersonID)
	if errorValue != nil {
		return graph
	}
	graph.Facts = appendUniqueFacts(graph.Facts, pinnedFacts)
	return graph
}

func appendUniqueFacts(facts []memory.MemoryFact, additions []memory.MemoryFact) []memory.MemoryFact {
	seenFactKeys := map[string]bool{}
	for _, fact := range facts {
		seenFactKeys[fact.NamespaceID+":"+fact.FactID+":"+fact.Content] = true
	}
	for _, fact := range additions {
		key := fact.NamespaceID + ":" + fact.FactID + ":" + fact.Content
		if seenFactKeys[key] {
			continue
		}
		seenFactKeys[key] = true
		facts = append(facts, fact)
	}
	return facts
}

func (memoryGraphHandler MemoryGraphHandler) filterGraphForReader(request *http.Request, graph memory.MemoryGraph) memory.MemoryGraph {
	readerPersonID := strings.TrimSpace(request.URL.Query().Get("readerPersonID"))
	if readerPersonID == "" {
		return graph
	}
	if memoryGraphHandler.Identity == nil {
		graph.Namespaces = []memory.MemoryGraphNamespace{}
		graph.Episodes = []memory.MemoryGraphEpisode{}
		graph.Nodes = []memory.MemoryGraphNode{}
		graph.Edges = []memory.MemoryGraphEdge{}
		return graph
	}
	personAccess := memoryGraphHandler.Identity.ResolvePersonAccess(readerPersonID)
	namespaces := filterReaderNamespaces(graph.Namespaces, readerPersonID, personAccess)
	namespaceSet := memoryNamespaceIDSet(namespaces)
	graph.Namespaces = namespaces
	graph.Episodes = filterReaderEpisodes(graph.Episodes, namespaceSet)
	graph.Nodes = []memory.MemoryGraphNode{}
	graph.Edges = []memory.MemoryGraphEdge{}
	return graph
}

func (memoryGraphHandler MemoryGraphHandler) attachSearchFacts(request *http.Request, graph memory.MemoryGraph) memory.MemoryGraph {
	query := strings.TrimSpace(request.URL.Query().Get("query"))
	if query == "" || memoryGraphHandler.MemoryService == nil {
		return graph
	}

	facts := memoryGraphHandler.searchNamespaceFacts(request, graph.Namespaces, query, memoryGraphLimit(request))
	graph.Facts = facts
	return graph
}

func (memoryGraphHandler MemoryGraphHandler) searchNamespaceFacts(request *http.Request, namespaces []memory.MemoryGraphNamespace, query string, limit int) []memory.MemoryFact {
	facts := []memory.MemoryFact{}
	seenFactKeys := map[string]bool{}
	for _, namespace := range namespaces {
		namespaceFacts, errorValue := memoryGraphHandler.MemoryService.SearchMemory(request.Context(), memory.MemorySearchRequest{
			Query:                   query,
			ReaderPersonID:          namespace.ScopePersonID,
			ReaderCircles:           namespaceReaderCircles(namespace),
			ReaderSecurityLevelRank: namespace.SecurityLevelRank,
			ReaderGrantedClasses:    namespace.RequiredClasses,
			Namespaces:              []memory.MemoryNamespace{memoryNamespaceFromGraphNamespace(namespace)},
			ExplicitNamespacesOnly:  true,
			Limit:                   limit,
		})
		if errorValue != nil {
			continue
		}
		for _, fact := range namespaceFacts {
			key := fact.NamespaceID + ":" + fact.FactID + ":" + fact.Content
			if key == "::" || seenFactKeys[key] {
				continue
			}
			seenFactKeys[key] = true
			facts = append(facts, fact)
			if len(facts) >= limit {
				return facts
			}
		}
	}
	return facts
}

func filterReaderNamespaces(namespaces []memory.MemoryGraphNamespace, readerPersonID string, personAccess policy.PersonAccess) []memory.MemoryGraphNamespace {
	filteredNamespaces := []memory.MemoryGraphNamespace{}
	for _, namespace := range namespaces {
		if canReadNamespace(namespace, readerPersonID, personAccess) {
			filteredNamespaces = append(filteredNamespaces, namespace)
		}
	}
	return filteredNamespaces
}

func canReadNamespace(namespace memory.MemoryGraphNamespace, readerPersonID string, personAccess policy.PersonAccess) bool {
	switch namespace.ScopeType {
	case memory.ScopeTypeUser, memory.ScopeTypePrivate:
		return strings.TrimSpace(namespace.ScopePersonID) == strings.TrimSpace(readerPersonID)
	case memory.ScopeTypeCircle:
		return containsMemoryCircle(personAccess.Circles, namespace.ScopeCircleID)
	case memory.ScopeTypeWorkspace:
		return personAccess.SecurityLevelRank >= namespace.SecurityLevelRank && containsMemoryClasses(personAccess.GrantedClasses, namespace.RequiredClasses)
	default:
		return false
	}
}

func filterReaderEpisodes(episodes []memory.MemoryGraphEpisode, namespaceSet map[string]bool) []memory.MemoryGraphEpisode {
	filteredEpisodes := []memory.MemoryGraphEpisode{}
	for _, episode := range episodes {
		namespaceIDs := []string{}
		for _, namespaceID := range episode.NamespaceIDs {
			if namespaceSet[namespaceID] {
				namespaceIDs = append(namespaceIDs, namespaceID)
			}
		}
		if len(namespaceIDs) == 0 {
			continue
		}
		episode.NamespaceIDs = namespaceIDs
		filteredEpisodes = append(filteredEpisodes, episode)
	}
	return filteredEpisodes
}

func memoryNamespaceIDSet(namespaces []memory.MemoryGraphNamespace) map[string]bool {
	namespaceSet := map[string]bool{}
	for _, namespace := range namespaces {
		namespaceSet[namespace.NamespaceID] = true
	}
	return namespaceSet
}

func containsMemoryCircle(circles []string, circleID string) bool {
	for _, value := range circles {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(circleID)) {
			return true
		}
	}
	return false
}

func containsMemoryClasses(grantedClasses []string, requiredClasses []string) bool {
	grantedSet := map[string]bool{}
	for _, grantedClass := range grantedClasses {
		grantedSet[strings.TrimSpace(grantedClass)] = true
	}
	for _, requiredClass := range requiredClasses {
		if !grantedSet[strings.TrimSpace(requiredClass)] {
			return false
		}
	}
	return true
}

func rebuildMemoryGraphTopology(graph memory.MemoryGraph) memory.MemoryGraph {
	nodes := []memory.MemoryGraphNode{}
	edges := []memory.MemoryGraphEdge{}
	for _, namespace := range graph.Namespaces {
		nodes = append(nodes, memory.MemoryGraphNode{
			NodeID:    "namespace:" + namespace.NamespaceID,
			Label:     namespace.NamespaceID,
			Kind:      "namespace",
			ScopeType: namespace.ScopeType,
		})
	}
	for _, episode := range graph.Episodes {
		episodeNodeID := "episode:" + episode.EpisodeID
		nodes = append(nodes, memory.MemoryGraphNode{
			NodeID: episodeNodeID,
			Label:  episode.Platform + " " + episode.OccurredAt.Format("01-02 15:04"),
			Kind:   "episode",
			Status: episode.IngestionStatus,
		})
		for _, namespaceID := range episode.NamespaceIDs {
			edges = append(edges, memory.MemoryGraphEdge{
				SourceID: "namespace:" + namespaceID,
				TargetID: episodeNodeID,
				Label:    "episode",
				Weight:   1,
			})
		}
	}
	for _, fact := range graph.Facts {
		factNodeID := "fact:" + fact.NamespaceID + ":" + fact.FactID
		nodes = append(nodes, memory.MemoryGraphNode{
			NodeID:    factNodeID,
			Label:     compactGraphLabel(fact.Content),
			Kind:      "fact",
			ScopeType: fact.ScopeType,
			Status:    fact.SourceKind,
		})
		edges = append(edges, memory.MemoryGraphEdge{
			SourceID: "namespace:" + fact.NamespaceID,
			TargetID: factNodeID,
			Label:    "fact",
			Weight:   2,
		})
	}
	graph.Nodes = nodes
	graph.Edges = edges
	return graph
}

func memoryGraphLimit(request *http.Request) int {
	limit, errorValue := strconv.Atoi(strings.TrimSpace(request.URL.Query().Get("limit")))
	if errorValue != nil || limit <= 0 {
		return 80
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func namespaceReaderCircles(namespace memory.MemoryGraphNamespace) []string {
	if namespace.ScopeCircleID == "" {
		return []string{}
	}
	return []string{namespace.ScopeCircleID}
}

func memoryNamespaceFromGraphNamespace(namespace memory.MemoryGraphNamespace) memory.MemoryNamespace {
	return memory.MemoryNamespace{
		NamespaceID:         namespace.NamespaceID,
		ScopeType:           namespace.ScopeType,
		ScopePersonID:       namespace.ScopePersonID,
		ScopeConversationID: namespace.ScopeConversationID,
		ScopeCircleID:       namespace.ScopeCircleID,
		SecurityLevelRank:   namespace.SecurityLevelRank,
		RequiredClasses:     namespace.RequiredClasses,
	}
}

func compactGraphLabel(value string) string {
	runes := []rune(strings.Join(strings.Fields(value), " "))
	if len(runes) <= 56 {
		return string(runes)
	}
	return string(runes[:56]) + "..."
}
