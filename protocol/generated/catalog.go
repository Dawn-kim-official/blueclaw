package capabilitycatalog

import (
	_ "embed"
	"encoding/json"
)

//go:embed capability-tools.json
var capabilityToolCatalog []byte

//go:embed manifest.json
var protocolManifest []byte

// ProtocolIdentity is the contract version this build was generated from. It is
// what Blueclaw expects every process on the protocol to report.
type ProtocolIdentity struct {
	ProtocolVersion       string `json:"protocolVersion"`
	AggregateProtocolHash string `json:"aggregateHash"`
}

func CapabilityToolCatalog() []byte {
	return append([]byte{}, capabilityToolCatalog...)
}

func BuiltProtocolIdentity() ProtocolIdentity {
	identity := ProtocolIdentity{}
	_ = json.Unmarshal(protocolManifest, &identity)
	return identity
}
