package capabilitycatalog

import _ "embed"

//go:embed capability-tools.json
var capabilityToolCatalog []byte

func CapabilityToolCatalog() []byte {
	return append([]byte{}, capabilityToolCatalog...)
}
