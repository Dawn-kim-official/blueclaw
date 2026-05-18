package actortest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"blueclaw/internal/security"
	"blueclaw/internal/workspacepath"
)

type DirectWorkspaceActorFactory struct {
	terminalService *security.TerminalSessionService
}

type DirectWorkspaceActor struct {
	identity        security.ExecutionIdentity
	terminalService *security.TerminalSessionService
}

func NewDirectWorkspaceActorFactory(terminalServices ...*security.TerminalSessionService) DirectWorkspaceActorFactory {
	return DirectWorkspaceActorFactory{terminalService: firstTerminalService(terminalServices)}
}

func firstTerminalService(terminalServices []*security.TerminalSessionService) *security.TerminalSessionService {
	if len(terminalServices) == 0 {
		return nil
	}
	return terminalServices[0]
}

func (factory DirectWorkspaceActorFactory) Requester(ctx context.Context, request security.WorkspaceActorRequest) (security.WorkspaceActor, error) {
	_ = ctx
	personAccess := request.PersonAccess
	if strings.TrimSpace(personAccess.PersonID) == "" {
		return nil, security.WorkspaceActorError{Operation: "requester", Stage: "identity", Code: security.ActorErrorCodeIdentityMissing, Detail: security.ActorErrorCodeIdentityMissing}
	}
	return DirectWorkspaceActor{
		identity:        security.ExecutionIdentityForPersonAccess(personAccess, request.WorkspaceRootPath),
		terminalService: factory.terminalService,
	}, nil
}

func (actor DirectWorkspaceActor) Run(ctx context.Context, commandRequest security.CommandRequest) (security.CommandResult, error) {
	if actor.terminalService == nil {
		return security.CommandResult{}, errors.New("direct test actor does not run shell commands")
	}
	if strings.TrimSpace(commandRequest.ExecutionIdentity.UserName) == "" {
		commandRequest.ExecutionIdentity = actor.identity
	}
	return actor.terminalService.RunCommand(ctx, commandRequest)
}

func (actor DirectWorkspaceActor) StartInteractiveSession(commandRequest security.CommandRequest) (string, error) {
	if actor.terminalService == nil {
		return "", errors.New("direct test actor does not start terminal sessions")
	}
	if strings.TrimSpace(commandRequest.ExecutionIdentity.UserName) == "" {
		commandRequest.ExecutionIdentity = actor.identity
	}
	return actor.terminalService.StartInteractiveSession(commandRequest)
}

func (actor DirectWorkspaceActor) MkdirAll(ctx context.Context, directory workspacepath.Directory, mode os.FileMode) error {
	_ = ctx
	if errorValue := os.MkdirAll(directory.ConcretePath, mode.Perm()); errorValue != nil {
		return actor.actorError("mkdir_all", "direct", workspacepath.Path(directory), errorValue)
	}
	if errorValue := os.Chmod(directory.ConcretePath, modeBits(mode)); errorValue != nil {
		return actor.actorError("mkdir_all", "direct", workspacepath.Path(directory), errorValue)
	}
	return nil
}

func (actor DirectWorkspaceActor) WriteFile(ctx context.Context, path workspacepath.Path, content []byte, mode os.FileMode) error {
	_ = ctx
	if errorValue := os.WriteFile(path.ConcretePath, content, mode.Perm()); errorValue != nil {
		return actor.actorError("write_file", "direct", path, errorValue)
	}
	if errorValue := os.Chmod(path.ConcretePath, mode.Perm()); errorValue != nil {
		return actor.actorError("write_file", "direct", path, errorValue)
	}
	return nil
}

func (actor DirectWorkspaceActor) ReadFile(ctx context.Context, path workspacepath.Path, maxBytes int64) ([]byte, error) {
	_ = ctx
	fileInformation, errorValue := os.Stat(path.ConcretePath)
	if errorValue != nil {
		return nil, actor.actorError("read_file", "direct", path, errorValue)
	}
	if !fileInformation.Mode().IsRegular() {
		return nil, actor.actorError("read_file", "direct", path, errors.New("path is not a regular file"))
	}
	if maxBytes > 0 && fileInformation.Size() > maxBytes {
		return nil, actor.actorError("read_file", "direct", path, errors.New("file is too large"))
	}
	document, errorValue := os.ReadFile(path.ConcretePath)
	if errorValue != nil {
		return nil, actor.actorError("read_file", "direct", path, errorValue)
	}
	return document, nil
}

func (actor DirectWorkspaceActor) CopyFile(ctx context.Context, source workspacepath.Path, destination workspacepath.Path, mode os.FileMode, overwrite bool) error {
	_ = ctx
	sourceFile, errorValue := os.Open(source.ConcretePath)
	if errorValue != nil {
		return actor.actorError("copy_file", "direct", source, errorValue)
	}
	defer sourceFile.Close()
	flags := os.O_CREATE | os.O_WRONLY
	if overwrite {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	destinationFile, errorValue := os.OpenFile(destination.ConcretePath, flags, mode.Perm())
	if errorValue != nil {
		return actor.actorError("copy_file", "direct", destination, errorValue)
	}
	_, copyError := io.Copy(destinationFile, sourceFile)
	closeError := destinationFile.Close()
	if copyError != nil {
		return actor.actorError("copy_file", "direct", destination, copyError)
	}
	if closeError != nil {
		return actor.actorError("copy_file", "direct", destination, closeError)
	}
	if errorValue := os.Chmod(destination.ConcretePath, mode.Perm()); errorValue != nil {
		return actor.actorError("copy_file", "direct", destination, errorValue)
	}
	return nil
}

func (actor DirectWorkspaceActor) BundleDirectory(ctx context.Context, directory workspacepath.Directory, options security.WorkspaceActorBundleOptions) (security.WorkspaceActorBundle, error) {
	_ = ctx
	document, errorValue := actor.bundleDirectoryDocument(workspacepath.Path(directory), options)
	if errorValue != nil {
		return security.WorkspaceActorBundle{}, errorValue
	}
	return security.WorkspaceActorBundle{
		Format:        "tar.gz",
		ContentBase64: base64.StdEncoding.EncodeToString(document),
		SizeBytes:     int64(len(document)),
	}, nil
}

func (actor DirectWorkspaceActor) bundleDirectoryDocument(path workspacepath.Path, options security.WorkspaceActorBundleOptions) ([]byte, error) {
	buffer := bytes.Buffer{}
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	errorValue := filepath.Walk(path.ConcretePath, func(currentPath string, information os.FileInfo, walkError error) error {
		if walkError != nil {
			return walkError
		}
		relativePath, errorValue := filepath.Rel(path.ConcretePath, currentPath)
		if errorValue != nil || relativePath == "." {
			return errorValue
		}
		if bundlePathIsExcluded(relativePath, information, options.ExcludeNames) {
			if information.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return writeBundleEntry(tarWriter, currentPath, relativePath, information)
	})
	closeError := tarWriter.Close()
	gzipCloseError := gzipWriter.Close()
	if errorValue != nil {
		return nil, actor.actorError("bundle_directory", "direct", path, errorValue)
	}
	if closeError != nil {
		return nil, actor.actorError("bundle_directory", "direct", path, closeError)
	}
	if gzipCloseError != nil {
		return nil, actor.actorError("bundle_directory", "direct", path, gzipCloseError)
	}
	if options.MaxBytes > 0 && int64(buffer.Len()) > options.MaxBytes {
		return nil, actor.actorError("bundle_directory", "direct", path, errors.New("directory bundle is too large"))
	}
	return buffer.Bytes(), nil
}

func bundlePathIsExcluded(relativePath string, information os.FileInfo, excludeNames []string) bool {
	pathParts := strings.Split(filepath.ToSlash(relativePath), "/")
	for _, pathPart := range pathParts {
		for _, excludeName := range excludeNames {
			if strings.TrimSpace(pathPart) == strings.TrimSpace(excludeName) {
				return true
			}
		}
	}
	return !information.IsDir() && strings.HasSuffix(relativePath, "~")
}

func writeBundleEntry(tarWriter *tar.Writer, path string, relativePath string, information os.FileInfo) error {
	header, errorValue := tar.FileInfoHeader(information, "")
	if errorValue != nil {
		return errorValue
	}
	header.Name = filepath.ToSlash(relativePath)
	if errorValue := tarWriter.WriteHeader(header); errorValue != nil {
		return errorValue
	}
	if !information.Mode().IsRegular() {
		return nil
	}
	file, errorValue := os.Open(path)
	if errorValue != nil {
		return errorValue
	}
	defer file.Close()
	_, errorValue = io.Copy(tarWriter, file)
	return errorValue
}

func (actor DirectWorkspaceActor) Stat(ctx context.Context, path workspacepath.Path) (security.WorkspaceActorStat, error) {
	_ = ctx
	fileInformation, errorValue := os.Stat(path.ConcretePath)
	if errorValue != nil {
		return security.WorkspaceActorStat{}, actor.actorError("stat", "direct", path, errorValue)
	}
	return security.WorkspaceActorStat{
		Path:        path,
		IsRegular:   fileInformation.Mode().IsRegular(),
		IsDirectory: fileInformation.IsDir(),
		SizeBytes:   fileInformation.Size(),
		Mode:        fileInformation.Mode(),
	}, nil
}

func (actor DirectWorkspaceActor) actorError(operation string, stage string, path workspacepath.Path, errorValue error) error {
	return security.WorkspaceActorError{
		Operation:   operation,
		Stage:       stage,
		ActorUser:   actor.identity.UserName,
		VirtualPath: path.VirtualPath,
		Code:        actorErrorCode(errorValue),
		Detail:      strings.ReplaceAll(errorValue.Error(), path.ConcretePath, path.VirtualPath),
	}
}

func actorErrorCode(errorValue error) string {
	if os.IsNotExist(errorValue) {
		return security.ActorErrorCodeNotFound
	}
	if os.IsPermission(errorValue) {
		return security.ActorErrorCodePermissionDenied
	}
	if os.IsExist(errorValue) {
		return security.ActorErrorCodeAlreadyExists
	}
	return security.ActorErrorCodeOperationFailed
}

func modeBits(mode os.FileMode) os.FileMode {
	return os.FileMode(uint32(mode) & 07777)
}
