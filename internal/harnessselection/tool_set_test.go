package harnessselection

import "github.com/yeomyeonggeori/bluecollar/toolcontract"

func emptyToolSet() *toolcontract.ToolSet {
	return toolcontract.NewToolSet(nil)
}
