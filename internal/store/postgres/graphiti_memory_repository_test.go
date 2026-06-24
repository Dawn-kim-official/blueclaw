package postgres

import (
	"reflect"
	"testing"
)

func TestStringSliceFromDocument(t *testing.T) {
	actual := stringSliceFromDocument(`["alpha"," beta ",""]`)
	expected := []string{"alpha", "beta"}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("stringSliceFromDocument() = %#v, want %#v", actual, expected)
	}
}

func TestStringSliceFromDocumentReturnsEmptySliceForInvalidDocument(t *testing.T) {
	actual := stringSliceFromDocument(`{alpha}`)
	if len(actual) != 0 {
		t.Fatalf("stringSliceFromDocument() = %#v, want empty slice", actual)
	}
}

func TestRemoveGraphEpisodeNamespacesRemovesOnlyRequestedNamespaces(t *testing.T) {
	actual, removed := removeGraphEpisodeNamespaces(
		`[{"namespaceID":"user:person-1","scopeType":"user"},{"namespaceID":"workspace:default","scopeType":"workspace"}]`,
		[]string{"user:person-1"},
	)
	if !removed {
		t.Fatal("expected namespace removal")
	}
	expectedNamespaceIDs := []string{"workspace:default"}
	actualNamespaceIDs := []string{}
	for _, namespace := range actual {
		actualNamespaceIDs = append(actualNamespaceIDs, namespace.NamespaceID)
	}
	if !reflect.DeepEqual(actualNamespaceIDs, expectedNamespaceIDs) {
		t.Fatalf("namespace ids = %#v, want %#v", actualNamespaceIDs, expectedNamespaceIDs)
	}
}
