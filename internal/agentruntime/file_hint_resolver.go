package agentruntime

import (
	"errors"
	"strings"

	"github.com/Dawn-kim-official/blueclaw/agentcontract"
)

func resolveFileHintReference(request ToolCatalogRequest, path string, materialID string, fileHint string) (string, string, error) {
	trimmedPath := strings.TrimSpace(path)
	trimmedMaterialID := strings.TrimSpace(materialID)
	trimmedFileHint := strings.TrimSpace(fileHint)
	if trimmedPath != "" || trimmedMaterialID != "" || trimmedFileHint == "" {
		return trimmedPath, trimmedMaterialID, nil
	}
	material, isFound := visibleAttachmentMaterialForFileHint(request.VisibleContext, trimmedFileHint)
	if !isFound {
		return "", "", errors.New("fileHint is not available in the current visible attachment catalog")
	}
	resolvedPath := strings.TrimSpace(material.Path)
	resolvedMaterialID := firstNonEmptyString(trimmedMaterialID, strings.TrimSpace(material.MaterialID))
	if resolvedPath == "" && resolvedMaterialID == "" {
		return "", "", errors.New("fileHint has no readable path or materialID")
	}
	return resolvedPath, resolvedMaterialID, nil
}

func visibleAttachmentMaterialForFileHint(visibleContext agentcontract.VisibleContext, fileHint string) (agentcontract.VisibleContextMaterial, bool) {
	trimmedFileHint := strings.TrimSpace(fileHint)
	if trimmedFileHint == "" {
		return agentcontract.VisibleContextMaterial{}, false
	}
	for _, material := range visibleAttachmentMaterials(visibleContext) {
		if strings.TrimSpace(material.FileHint) == trimmedFileHint {
			return material, true
		}
	}
	return agentcontract.VisibleContextMaterial{}, false
}
