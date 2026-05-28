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
