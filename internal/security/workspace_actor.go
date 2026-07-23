package security

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"blueclaw/internal/config"
	"blueclaw/internal/policy"
	"blueclaw/internal/workspacepath"
)

const (
	ActorErrorCodeIdentityMissing      = "actor_identity_missing"
	ActorErrorCodeRuntimeUnavailable   = "actor_runtime_unavailable"
	ActorErrorCodePermissionDenied     = "permission_denied"
	ActorErrorCodeNotFound             = "not_found"
	ActorErrorCodeAlreadyExists        = "already_exists"
	ActorErrorCodeInvalidPath          = "invalid_path"
	ActorErrorCodeOperationFailed      = "operation_failed"
	ActorErrorCodeUnsupportedOperation = "unsupported_operation"
)

type WorkspaceActor interface {
	Run(context.Context, CommandRequest) (CommandResult, error)
	StartInteractiveSession(CommandRequest) (string, error)
	MkdirAll(context.Context, workspacepath.Directory, os.FileMode) error
	WriteFile(context.Context, workspacepath.Path, []byte, os.FileMode) error
	ReadFile(context.Context, workspacepath.Path, int64) ([]byte, error)
	CopyFile(context.Context, workspacepath.Path, workspacepath.Path, os.FileMode, bool) error
	BundleDirectory(context.Context, workspacepath.Directory, WorkspaceActorBundleOptions) (WorkspaceActorBundle, error)
	Stat(context.Context, workspacepath.Path) (WorkspaceActorStat, error)
}

type WorkspaceActorFactory interface {
	Requester(context.Context, WorkspaceActorRequest) (WorkspaceActor, error)
}

type WorkspaceActorRequest struct {
	PersonAccess      policy.PersonAccess
	WorkspaceRootPath string
}

type WorkspaceActorError struct {
	Operation   string `json:"operation"`
	Stage       string `json:"stage"`
	ActorUser   string `json:"actorUser"`
	VirtualPath string `json:"virtualPath"`
	Code        string `json:"code"`
	Detail      string `json:"detail"`
}

type WorkspaceActorStat struct {
	Path        workspacepath.Path `json:"path"`
	IsRegular   bool               `json:"isRegular"`
	IsDirectory bool               `json:"isDirectory"`
	SizeBytes   int64              `json:"sizeBytes"`
	Mode        os.FileMode        `json:"mode"`
}

type WorkspaceActorBundleOptions struct {
	Format       string
	MaxBytes     int64
	ExcludeNames []string
}

type WorkspaceActorBundle struct {
	Format        string
	ContentBase64 string
	SizeBytes     int64
}

type POSIXWorkspaceActorFactory struct {
	terminalService       *TerminalSessionService
	terminalConfiguration config.TerminalConfiguration
}

type POSIXHelperWorkspaceActor struct {
	terminalService       *TerminalSessionService
	terminalConfiguration config.TerminalConfiguration
	executionIdentity     ExecutionIdentity
}

func (actorError WorkspaceActorError) Error() string {
	detail := strings.TrimSpace(actorError.Detail)
	if detail == "" {
		detail = "operation failed"
	}
	actorUser := strings.TrimSpace(actorError.ActorUser)
	if actorUser == "" {
		actorUser = "unknown"
	}
	virtualPath := strings.TrimSpace(actorError.VirtualPath)
	if virtualPath == "" {
		virtualPath = "workspace path"
	}
	return fmt.Sprintf("actor.%s failed for %s as %s: %s", actorError.Operation, virtualPath, actorUser, detail)
}

func (terminalSessionService *TerminalSessionService) WorkspaceActorFactory() WorkspaceActorFactory {
	return POSIXWorkspaceActorFactory{
		terminalService:       terminalSessionService,
		terminalConfiguration: terminalSessionService.commandGuardrailService.terminalConfiguration,
	}
}

func (factory POSIXWorkspaceActorFactory) Requester(ctx context.Context, request WorkspaceActorRequest) (WorkspaceActor, error) {
	identity := ExecutionIdentityForPersonAccess(request.PersonAccess, request.WorkspaceRootPath)
	if strings.TrimSpace(identity.UserName) == "" {
		return nil, WorkspaceActorError{Operation: "requester", Stage: "identity", Code: ActorErrorCodeIdentityMissing, Detail: ActorErrorCodeIdentityMissing}
	}
	if factory.terminalService == nil || strings.TrimSpace(factory.terminalConfiguration.POSIXHelperPath) == "" {
		return nil, WorkspaceActorError{Operation: "requester", Stage: "factory", ActorUser: identity.UserName, Code: ActorErrorCodeRuntimeUnavailable, Detail: "posix helper is required for requester workspace side effects"}
	}
	if errorValue := ensureHelperSupportsFS(ctx, factory.terminalConfiguration.POSIXHelperPath, identity.UserName); errorValue != nil {
		return nil, errorValue
	}
	return POSIXHelperWorkspaceActor{
		terminalService:       factory.terminalService,
		terminalConfiguration: factory.terminalConfiguration,
		executionIdentity:     identity,
	}, nil
}

type helperCapabilities struct {
	Version      int      `json:"version"`
	Capabilities []string `json:"capabilities"`
}

var verifiedHelperFSSupportByPath sync.Map

func ensureHelperSupportsFS(ctx context.Context, helperPath string, actorUser string) error {
	if _, isVerified := verifiedHelperFSSupportByPath.Load(helperPath); isVerified {
		return nil
	}
	if errorValue := probeHelperSupportsFS(ctx, helperPath, actorUser); errorValue != nil {
		return errorValue
	}
	verifiedHelperFSSupportByPath.Store(helperPath, true)
	return nil
}

func probeHelperSupportsFS(ctx context.Context, helperPath string, actorUser string) error {
	executionContext, cancelFunction := context.WithTimeout(ctx, 15*time.Second)
	defer cancelFunction()
	command := exec.CommandContext(executionContext, helperPath, "capabilities")
	output, errorValue := command.CombinedOutput()
	if executionContext.Err() == context.DeadlineExceeded {
		return WorkspaceActorError{Operation: "requester", Stage: "capabilities", ActorUser: actorUser, Code: ActorErrorCodeRuntimeUnavailable, Detail: helperFailureDetail(helperPath, "capabilities", "posix helper capabilities timed out", nil)}
	}
	if errorValue != nil {
		return WorkspaceActorError{Operation: "requester", Stage: "capabilities", ActorUser: actorUser, Code: ActorErrorCodeRuntimeUnavailable, Detail: helperFailureDetail(helperPath, "capabilities", errorValue.Error(), output)}
	}
	var capabilities helperCapabilities
	if errorValue := json.Unmarshal(output, &capabilities); errorValue != nil {
		return WorkspaceActorError{Operation: "requester", Stage: "capabilities", ActorUser: actorUser, Code: ActorErrorCodeRuntimeUnavailable, Detail: helperFailureDetail(helperPath, "capabilities", errorValue.Error(), output)}
	}
	if capabilities.Version < 2 || !containsCapability(capabilities.Capabilities, "fs") {
		return WorkspaceActorError{Operation: "requester", Stage: "capabilities", ActorUser: actorUser, Code: ActorErrorCodeRuntimeUnavailable, Detail: helperFailureDetail(helperPath, "capabilities", "posix helper does not support fs capability", output)}
	}
	return nil
}

func containsCapability(capabilities []string, expectedCapability string) bool {
	for _, capability := range capabilities {
		if strings.TrimSpace(capability) == expectedCapability {
			return true
		}
	}
	return false
}

func (actor POSIXHelperWorkspaceActor) Run(ctx context.Context, commandRequest CommandRequest) (CommandResult, error) {
	if strings.TrimSpace(commandRequest.ExecutionIdentity.UserName) == "" {
		commandRequest.ExecutionIdentity = actor.executionIdentity
	}
	if strings.TrimSpace(commandRequest.ExecutionIdentity.UserName) == "" {
		return CommandResult{}, actorError("run", "identity", actor.executionIdentity, workspacepath.Path{}, ActorErrorCodeIdentityMissing, ActorErrorCodeIdentityMissing)
	}
	return actor.terminalService.RunCommand(ctx, commandRequest)
}

func (actor POSIXHelperWorkspaceActor) StartInteractiveSession(commandRequest CommandRequest) (string, error) {
	if strings.TrimSpace(commandRequest.ExecutionIdentity.UserName) == "" {
		commandRequest.ExecutionIdentity = actor.executionIdentity
	}
	if strings.TrimSpace(commandRequest.ExecutionIdentity.UserName) == "" {
		return "", actorError("start_session", "identity", actor.executionIdentity, workspacepath.Path{}, ActorErrorCodeIdentityMissing, ActorErrorCodeIdentityMissing)
	}
	return actor.terminalService.StartInteractiveSession(commandRequest)
}

func (actor POSIXHelperWorkspaceActor) MkdirAll(ctx context.Context, directory workspacepath.Directory, mode os.FileMode) error {
	path := workspacepath.Path(directory)
	return actor.executeFS(ctx, "mkdir_all", path, fsRequest{Path: path.ConcretePath, Mode: modeBits(mode)}, nil)
}

func (actor POSIXHelperWorkspaceActor) WriteFile(ctx context.Context, path workspacepath.Path, content []byte, mode os.FileMode) error {
	return actor.executeFS(ctx, "write_file", path, fsRequest{Path: path.ConcretePath, Mode: mode.Perm()}, bytes.NewReader(content))
}

func (actor POSIXHelperWorkspaceActor) ReadFile(ctx context.Context, path workspacepath.Path, maxBytes int64) ([]byte, error) {
	var response fsResponse
	errorValue := actor.executeFSWithResponse(ctx, "read_file", path, fsRequest{Path: path.ConcretePath, MaxBytes: maxBytes}, nil, &response)
	if errorValue != nil {
		return nil, errorValue
	}
	document, errorValue := base64.StdEncoding.DecodeString(response.ContentBase64)
	if errorValue != nil {
		return nil, actorError("read_file", "decode", actor.executionIdentity, path, ActorErrorCodeOperationFailed, errorValue.Error())
	}
	return document, nil
}

func (actor POSIXHelperWorkspaceActor) CopyFile(ctx context.Context, source workspacepath.Path, destination workspacepath.Path, mode os.FileMode, overwrite bool) error {
	return actor.executeFS(ctx, "copy_file", destination, fsRequest{
		Source:    source.ConcretePath,
		Path:      destination.ConcretePath,
		Mode:      mode.Perm(),
		Overwrite: overwrite,
	}, nil)
}

func (actor POSIXHelperWorkspaceActor) BundleDirectory(ctx context.Context, directory workspacepath.Directory, options WorkspaceActorBundleOptions) (WorkspaceActorBundle, error) {
	path := workspacepath.Path(directory)
	var response fsResponse
	errorValue := actor.executeFSWithResponse(ctx, "bundle_directory", path, fsRequest{
		Path:         path.ConcretePath,
		MaxBytes:     options.MaxBytes,
		ExcludeNames: options.ExcludeNames,
	}, nil, &response)
	if errorValue != nil {
		return WorkspaceActorBundle{}, errorValue
	}
	return WorkspaceActorBundle{
		Format:        firstNonEmptyString(response.Format, "tar.gz"),
		ContentBase64: response.ContentBase64,
		SizeBytes:     response.SizeBytes,
	}, nil
}

func (actor POSIXHelperWorkspaceActor) Stat(ctx context.Context, path workspacepath.Path) (WorkspaceActorStat, error) {
	var response fsResponse
	errorValue := actor.executeFSWithResponse(ctx, "stat", path, fsRequest{Path: path.ConcretePath}, nil, &response)
	if errorValue != nil {
		return WorkspaceActorStat{}, errorValue
	}
	return WorkspaceActorStat{
		Path:        path,
		IsRegular:   response.IsRegular,
		IsDirectory: response.IsDirectory,
		SizeBytes:   response.SizeBytes,
		Mode:        response.Mode,
	}, nil
}

func (actor POSIXHelperWorkspaceActor) executeFS(ctx context.Context, operation string, path workspacepath.Path, request fsRequest, stdin io.Reader) error {
	return actor.executeFSWithResponse(ctx, operation, path, request, stdin, nil)
}

func (actor POSIXHelperWorkspaceActor) executeFSWithResponse(ctx context.Context, operation string, path workspacepath.Path, request fsRequest, stdin io.Reader, response *fsResponse) error {
	resolvedIdentity, errorValue := ResolveExecutionIdentity(actor.executionIdentity)
	if errorValue != nil {
		return actorError(operation, "resolve_identity", actor.executionIdentity, path, ActorErrorCodeIdentityMissing, errorValue.Error())
	}
	arguments := fsHelperArguments(operation, resolvedIdentity, request)
	executionContext, cancelFunction := context.WithTimeout(ctx, fsHelperOperationTimeout(operation))
	defer cancelFunction()
	command := exec.CommandContext(executionContext, actor.terminalConfiguration.POSIXHelperPath, arguments...)
	if stdin != nil {
		command.Stdin = stdin
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	errorValue = command.Run()
	if executionContext.Err() == context.DeadlineExceeded {
		return actorError(operation, "helper", actor.executionIdentity, path, ActorErrorCodeOperationFailed, "operation timed out")
	}
	if errorValue != nil {
		detail := firstNonEmptyString(strings.TrimSpace(stderr.String()), errorValue.Error())
		code := actorErrorCodeForDetail(detail)
		if isHelperExecutionFailure(errorValue, stderr.String()) {
			code = ActorErrorCodeRuntimeUnavailable
			detail = helperFailureDetail(actor.terminalConfiguration.POSIXHelperPath, operation, detail, nil)
		}
		return actorError(operation, "helper", actor.executionIdentity, path, code, detail)
	}
	if response != nil {
		if errorValue := json.Unmarshal(stdout.Bytes(), response); errorValue != nil {
			return actorError(operation, "decode", actor.executionIdentity, path, ActorErrorCodeOperationFailed, errorValue.Error())
		}
	}
	return nil
}

func helperFailureDetail(helperPath string, operation string, detail string, output []byte) string {
	parts := []string{
		"posix helper " + strings.TrimSpace(operation) + " failed",
		"path=" + strings.TrimSpace(helperPath),
	}
	if trimmedOutput := strings.TrimSpace(string(output)); trimmedOutput != "" {
		parts = append(parts, "output="+trimmedOutput)
	}
	if trimmedDetail := strings.TrimSpace(detail); trimmedDetail != "" {
		parts = append(parts, "detail="+trimmedDetail)
	}
	return strings.Join(parts, "; ")
}

func isHelperExecutionFailure(errorValue error, stderr string) bool {
	return strings.TrimSpace(stderr) == "" && os.IsPermission(errorValue)
}

type fsRequest struct {
	Path         string
	Source       string
	Mode         os.FileMode
	MaxBytes     int64
	Overwrite    bool
	ExcludeNames []string
}

type fsResponse struct {
	IsRegular     bool        `json:"isRegular"`
	IsDirectory   bool        `json:"isDirectory"`
	SizeBytes     int64       `json:"sizeBytes"`
	Mode          os.FileMode `json:"mode"`
	ContentBase64 string      `json:"contentBase64"`
	Format        string      `json:"format"`
}

func fsHelperOperationTimeout(operation string) time.Duration {
	if operation == "bundle_directory" {
		return 180 * time.Second
	}
	return 30 * time.Second
}

func fsHelperArguments(operation string, identity ExecutionIdentity, request fsRequest) []string {
	arguments := []string{
		"fs",
		"--uid", formatUnsignedID(identity.UserID),
		"--gid", formatUnsignedID(identity.GroupID),
		"--groups", joinUnsignedIDs(identity.SupplementaryGroupIDs),
		"--operation", operation,
	}
	if strings.TrimSpace(request.Path) != "" {
		arguments = append(arguments, "--path", request.Path)
	}
	if strings.TrimSpace(request.Source) != "" {
		arguments = append(arguments, "--source", request.Source)
	}
	if request.Mode != 0 {
		arguments = append(arguments, "--mode", fmt.Sprintf("%04o", request.Mode))
	}
	if request.MaxBytes > 0 {
		arguments = append(arguments, "--max-bytes", fmt.Sprintf("%d", request.MaxBytes))
	}
	if request.Overwrite {
		arguments = append(arguments, "--overwrite")
	}
	if len(request.ExcludeNames) > 0 {
		arguments = append(arguments, "--exclude-names", strings.Join(request.ExcludeNames, ","))
	}
	return arguments
}

func actorError(operation string, stage string, identity ExecutionIdentity, path workspacepath.Path, code string, detail string) WorkspaceActorError {
	return WorkspaceActorError{
		Operation:   operation,
		Stage:       stage,
		ActorUser:   strings.TrimSpace(identity.UserName),
		VirtualPath: strings.TrimSpace(path.VirtualPath),
		Code:        firstNonEmptyString(code, ActorErrorCodeOperationFailed),
		Detail:      firstNonEmptyString(redactWorkspacePathDetail(detail, path), "operation failed"),
	}
}

func actorErrorCodeForDetail(detail string) string {
	normalizedDetail := strings.ToLower(strings.TrimSpace(detail))
	switch {
	case strings.Contains(normalizedDetail, "permission denied"):
		return ActorErrorCodePermissionDenied
	case strings.Contains(normalizedDetail, "no such file or directory"):
		return ActorErrorCodeNotFound
	case strings.Contains(normalizedDetail, "file exists"):
		return ActorErrorCodeAlreadyExists
	case strings.Contains(normalizedDetail, "not a regular file"):
		return ActorErrorCodeInvalidPath
	default:
		return ActorErrorCodeOperationFailed
	}
}

func redactWorkspacePathDetail(detail string, path workspacepath.Path) string {
	trimmedDetail := strings.TrimSpace(detail)
	if strings.TrimSpace(path.ConcretePath) == "" || strings.TrimSpace(path.VirtualPath) == "" {
		return trimmedDetail
	}
	return strings.ReplaceAll(trimmedDetail, path.ConcretePath, path.VirtualPath)
}

func modeBits(mode os.FileMode) os.FileMode {
	return os.FileMode(uint32(mode) & 07777)
}

func IsActorNotFoundError(errorValue error) bool {
	var actorError WorkspaceActorError
	if errors.As(errorValue, &actorError) {
		return actorError.Code == ActorErrorCodeNotFound
	}
	return false
}
