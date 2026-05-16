package workspacepath

import (
	"path/filepath"
)

type Kind string

const (
	KindDraft        Kind = "draft"
	KindArtifact     Kind = "artifact"
	KindCircle       Kind = "circle"
	KindSharedPublic Kind = "shared_public"
	KindSkills       Kind = "skills"
	KindWorkspace    Kind = "workspace"
)

type Path struct {
	ConcretePath      string
	VirtualPath       string
	Kind              Kind
	IsDurableArtifact bool
}

type Directory Path

func (path Path) Parent() Directory {
	return Directory{
		ConcretePath:      filepath.Dir(path.ConcretePath),
		VirtualPath:       filepath.ToSlash(filepath.Dir(path.VirtualPath)),
		Kind:              path.Kind,
		IsDurableArtifact: path.IsDurableArtifact,
	}
}

func (path Path) BaseName() string {
	return filepath.Base(path.ConcretePath)
}

func (directory Directory) JoinVirtualFile(name string) Path {
	return Path{
		ConcretePath:      filepath.Join(directory.ConcretePath, filepath.Base(name)),
		VirtualPath:       filepath.ToSlash(filepath.Join(directory.VirtualPath, filepath.Base(name))),
		Kind:              directory.Kind,
		IsDurableArtifact: directory.IsDurableArtifact,
	}
}
