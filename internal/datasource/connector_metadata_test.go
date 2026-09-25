package datasource

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestConnectorMetadataIncludesTopicSearch guards that the topic search connector
// stays discoverable through /api/v1/datasource/types: without its metadata entry
// the type exists in the registry but no client can offer it.
func TestConnectorMetadataIncludesTopicSearch(t *testing.T) {
	meta, ok := ConnectorMetadataRegistry[types.ConnectorTypeTopicSearch]
	if !ok {
		t.Fatalf("ConnectorMetadataRegistry is missing %q", types.ConnectorTypeTopicSearch)
	}
	if meta.Name == "" || meta.AuthType != "none" {
		t.Fatalf("metadata = %+v, want a named connector with auth_type=none", meta)
	}
	var found bool
	for _, m := range ListAvailableConnectors() {
		if m.Type == types.ConnectorTypeTopicSearch {
			found = true
		}
	}
	if !found {
		t.Fatal("ListAvailableConnectors() does not expose topic_search")
	}
}
