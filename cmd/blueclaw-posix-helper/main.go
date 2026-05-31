package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"blueclaw/internal/policy"
	"blueclaw/internal/security"
)

func main() {
	if len(os.Args) < 2 {
		exitWithError(errors.New("blueclaw-posix-helper requires sync, exec, fs, or capabilities"))
	}

	var errorValue error
	switch os.Args[1] {
	case "capabilities":
		errorValue = runCapabilities()
	case "sync":
		errorValue = runAuthorized(runSync, os.Args[2:])
	case "exec":
		errorValue = runAuthorized(runExec, os.Args[2:])
	case "fs":
		errorValue = runAuthorized(runFS, os.Args[2:])
	default:
		errorValue = fmt.Errorf("unsupported command %q", os.Args[1])
	}
	if errorValue != nil {
		exitWithError(errorValue)
	}
}

func runAuthorized(run func([]string) error, arguments []string) error {
	if errorValue := authorizeHelperCaller(os.Getuid()); errorValue != nil {
		return errorValue
	}
	return run(arguments)
}

func runCapabilities() error {
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"version":      2,
		"capabilities": []string{"exec", "fs"},
	})
}

func authorizeHelperCaller(realUserID int) error {
	blueclawUserID, errorValue := lookupUserID("blueclaw")
	if errorValue != nil {
		return errorValue
	}
	if isAuthorizedHelperCaller(realUserID, blueclawUserID) {
		return nil
	}
	return fmt.Errorf("unauthorized helper caller: realUID=%d; expected root or blueclaw", realUserID)
}

func isAuthorizedHelperCaller(realUserID int, blueclawUserID int) bool {
	return realUserID == 0 || realUserID == blueclawUserID
}

func lookupUserID(userName string) (int, error) {
	resolvedUser, errorValue := user.Lookup(userName)
	if errorValue != nil {
		return 0, errorValue
	}
	userID, errorValue := strconv.Atoi(resolvedUser.Uid)
	if errorValue != nil {
		return 0, errorValue
	}
	return userID, nil
}

func runSync(arguments []string) error {
	flags := flag.NewFlagSet("sync", flag.ContinueOnError)
	policyPath := flags.String("policy", "/workspace/.blueclaw/config/policy.json", "policy path")
	workspacePath := flags.String("workspace", "/workspace", "workspace root path")
	if errorValue := flags.Parse(arguments); errorValue != nil {
		return errorValue
	}

	document, errorValue := os.ReadFile(*policyPath)
	if errorValue != nil {
		return errorValue
	}
	var policyDocument policy.PolicyDocument
	if errorValue := json.Unmarshal(document, &policyDocument); errorValue != nil {
		return errorValue
	}

	return applyPOSIXState(security.POSIXStateForPolicy(policyDocument, *workspacePath))
}

func runExec(arguments []string) error {
	flags := flag.NewFlagSet("exec", flag.ContinueOnError)
	userID := flags.Uint("uid", 0, "user id")
	groupID := flags.Uint("gid", 0, "group id")
	groupIDsDocument := flags.String("groups", "", "comma-separated supplementary group ids")
	workingDirectoryPath := flags.String("cwd", "", "working directory path")
	if errorValue := flags.Parse(arguments); errorValue != nil {
		return errorValue
	}
	if *userID == 0 || *groupID == 0 {
		return errors.New("uid and gid are required")
	}
	if strings.TrimSpace(*workingDirectoryPath) == "" {
		return errors.New("cwd is required")
	}
	executableArguments := flags.Args()
	if len(executableArguments) == 0 {
		return errors.New("executable path is required")
	}

	groupIDs, errorValue := parseGroupIDs(*groupIDsDocument)
	if errorValue != nil {
		return errorValue
	}
	if errorValue := prepareExecProcess(*userID, *groupID, groupIDs, *workingDirectoryPath, applyIdentity, os.Chdir); errorValue != nil {
		return errorValue
	}
	return syscall.Exec(executableArguments[0], executableArguments, canonicalExecEnvironment(os.Environ()))
}

func canonicalExecEnvironment(environment []string) []string {
	result := []string{"PATH=" + security.CanonicalRuntimePATH}
	for _, value := range environment {
		if strings.HasPrefix(value, "PATH=") {
			continue
		}
		result = append(result, value)
	}
	return result
}

func prepareExecProcess(userID uint, groupID uint, groupIDs []int, workingDirectoryPath string, applyIdentityFunction func(uint, uint, []int) error, changeDirectoryFunction func(string) error) error {
	if errorValue := applyIdentityFunction(userID, groupID, groupIDs); errorValue != nil {
		return errorValue
	}
	return changeDirectoryFunction(workingDirectoryPath)
}

func runFS(arguments []string) error {
	flags := flag.NewFlagSet("fs", flag.ContinueOnError)
	userID := flags.Uint("uid", 0, "user id")
	groupID := flags.Uint("gid", 0, "group id")
	groupIDsDocument := flags.String("groups", "", "comma-separated supplementary group ids")
	operation := flags.String("operation", "", "filesystem operation")
	path := flags.String("path", "", "target path")
	source := flags.String("source", "", "source path")
	modeText := flags.String("mode", "0660", "octal mode")
	maxBytes := flags.Int64("max-bytes", 0, "maximum read bytes")
	overwrite := flags.Bool("overwrite", false, "overwrite destination")
	excludeNamesDocument := flags.String("exclude-names", "", "comma-separated path names to skip")
	if errorValue := flags.Parse(arguments); errorValue != nil {
		return errorValue
	}
	if *userID == 0 || *groupID == 0 {
		return errors.New("uid and gid are required")
	}
	groupIDs, errorValue := parseGroupIDs(*groupIDsDocument)
	if errorValue != nil {
		return errorValue
	}
	mode, errorValue := parseFileMode(*modeText)
	if errorValue != nil {
		return errorValue
	}
	if errorValue := applyIdentity(*userID, *groupID, groupIDs); errorValue != nil {
		return errorValue
	}
	return performFSOperation(fsOperationRequest{
		Operation:    *operation,
		Path:         *path,
		Source:       *source,
		Mode:         mode,
		MaxBytes:     *maxBytes,
		Overwrite:    *overwrite,
		ExcludeNames: splitCommaSeparatedNames(*excludeNamesDocument),
	})
}

type fsOperationRequest struct {
	Operation    string
	Path         string
	Source       string
	Mode         os.FileMode
	MaxBytes     int64
	Overwrite    bool
	ExcludeNames []string
}

type fsOperationResponse struct {
	Path          string      `json:"path,omitempty"`
	IsRegular     bool        `json:"isRegular,omitempty"`
	IsDirectory   bool        `json:"isDirectory,omitempty"`
	SizeBytes     int64       `json:"sizeBytes,omitempty"`
	Mode          os.FileMode `json:"mode,omitempty"`
	ContentBase64 string      `json:"contentBase64,omitempty"`
	Format        string      `json:"format,omitempty"`
}

func performFSOperation(request fsOperationRequest) error {
	switch strings.TrimSpace(request.Operation) {
	case "mkdir_all":
		return mkdirAll(request.Path, request.Mode)
	case "write_file":
		return writeFile(request.Path, request.Mode)
	case "read_file":
		return readFile(request.Path, request.MaxBytes)
	case "copy_file":
		return copyFile(request.Source, request.Path, request.Mode, request.Overwrite)
	case "bundle_directory":
		return bundleDirectory(request.Path, request.MaxBytes, request.ExcludeNames)
	case "stat":
		return statPath(request.Path)
	default:
		return errors.New("unsupported fs operation")
	}
}

func applyIdentity(userID uint, groupID uint, groupIDs []int) error {
	if errorValue := syscall.Setgroups(groupIDs); errorValue != nil {
		return errorValue
	}
	if errorValue := syscall.Setgid(int(groupID)); errorValue != nil {
		return errorValue
	}
	return syscall.Setuid(int(userID))
}

func mkdirAll(path string, mode os.FileMode) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("path is required")
	}
	directories, errorValue := missingDirectoryChain(path)
	if errorValue != nil {
		return errorValue
	}
	if errorValue := os.MkdirAll(path, modeBits(mode)); errorValue != nil {
		return errorValue
	}
	for _, directory := range directories {
		if errorValue := os.Chmod(directory, modeBits(mode)); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func missingDirectoryChain(path string) ([]string, error) {
	cleanPath := filepath.Clean(path)
	directories := []string{}
	for currentPath := cleanPath; currentPath != "." && currentPath != string(filepath.Separator); currentPath = filepath.Dir(currentPath) {
		fileInformation, errorValue := os.Stat(currentPath)
		if errorValue == nil {
			if !fileInformation.IsDir() {
				return nil, fmt.Errorf("%s is not a directory", currentPath)
			}
			break
		}
		if !os.IsNotExist(errorValue) {
			return nil, errorValue
		}
		directories = append([]string{currentPath}, directories...)
	}
	if len(directories) == 0 {
		directories = append(directories, cleanPath)
	}
	return directories, nil
}

func writeFile(path string, mode os.FileMode) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("path is required")
	}
	document, errorValue := io.ReadAll(os.Stdin)
	if errorValue != nil {
		return errorValue
	}
	if errorValue := os.WriteFile(path, document, mode.Perm()); errorValue != nil {
		return errorValue
	}
	return os.Chmod(path, mode.Perm())
}

func readFile(path string, maxBytes int64) error {
	fileInformation, errorValue := regularFileInformation(path)
	if errorValue != nil {
		return errorValue
	}
	if maxBytes > 0 && fileInformation.Size() > maxBytes {
		return errors.New("file is too large")
	}
	document, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return errorValue
	}
	return json.NewEncoder(os.Stdout).Encode(fsOperationResponse{
		ContentBase64: base64.StdEncoding.EncodeToString(document),
		SizeBytes:     fileInformation.Size(),
	})
}

func copyFile(source string, destination string, mode os.FileMode, overwrite bool) error {
	if strings.TrimSpace(source) == "" || strings.TrimSpace(destination) == "" {
		return errors.New("source and path are required")
	}
	sourceFile, errorValue := os.Open(source)
	if errorValue != nil {
		return errorValue
	}
	defer sourceFile.Close()
	flags := os.O_CREATE | os.O_WRONLY
	if overwrite {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	destinationFile, errorValue := os.OpenFile(destination, flags, mode.Perm())
	if errorValue != nil {
		return errorValue
	}
	_, copyError := io.Copy(destinationFile, sourceFile)
	closeError := destinationFile.Close()
	if copyError != nil {
		return copyError
	}
	if closeError != nil {
		return closeError
	}
	return os.Chmod(destination, mode.Perm())
}

func statPath(path string) error {
	fileInformation, errorValue := os.Stat(path)
	if errorValue != nil {
		return errorValue
	}
	return json.NewEncoder(os.Stdout).Encode(fsOperationResponse{
		Path:        path,
		IsRegular:   fileInformation.Mode().IsRegular(),
		IsDirectory: fileInformation.IsDir(),
		SizeBytes:   fileInformation.Size(),
		Mode:        fileInformation.Mode(),
	})
}

func bundleDirectory(path string, maxBytes int64, excludeNames []string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("path is required")
	}
	fileInformation, errorValue := os.Stat(path)
	if errorValue != nil {
		return errorValue
	}
	if !fileInformation.IsDir() {
		return errors.New("path is not a directory")
	}
	document, errorValue := bundleDirectoryDocument(path, maxBytes, excludeNames)
	if errorValue != nil {
		return errorValue
	}
	return json.NewEncoder(os.Stdout).Encode(fsOperationResponse{
		ContentBase64: base64.StdEncoding.EncodeToString(document),
		SizeBytes:     int64(len(document)),
		Format:        "tar.gz",
	})
}

func bundleDirectoryDocument(rootPath string, maxBytes int64, excludeNames []string) ([]byte, error) {
	buffer := bytes.Buffer{}
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	errorValue := filepath.Walk(rootPath, func(path string, information os.FileInfo, walkError error) error {
		if walkError != nil {
			return walkError
		}
		relativePath, errorValue := filepath.Rel(rootPath, path)
		if errorValue != nil || relativePath == "." {
			return errorValue
		}
		if bundlePathIsExcluded(relativePath, information, excludeNames) {
			if information.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return writeBundleEntry(tarWriter, path, relativePath, information)
	})
	closeError := tarWriter.Close()
	gzipCloseError := gzipWriter.Close()
	if errorValue != nil {
		return nil, errorValue
	}
	if closeError != nil {
		return nil, closeError
	}
	if gzipCloseError != nil {
		return nil, gzipCloseError
	}
	if maxBytes > 0 && int64(buffer.Len()) > maxBytes {
		return nil, errors.New("directory bundle is too large")
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

func regularFileInformation(path string) (os.FileInfo, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("path is required")
	}
	fileInformation, errorValue := os.Stat(path)
	if errorValue != nil {
		return nil, errorValue
	}
	if !fileInformation.Mode().IsRegular() {
		return nil, errors.New("path is not a regular file")
	}
	return fileInformation, nil
}

func splitCommaSeparatedNames(document string) []string {
	names := []string{}
	for _, value := range strings.Split(document, ",") {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			names = append(names, trimmedValue)
		}
	}
	return names
}

func applyPOSIXState(state security.POSIXState) error {
	for _, group := range state.Groups {
		if errorValue := ensureGroup(group.Name); errorValue != nil {
			return errorValue
		}
	}
	for _, user := range state.Users {
		if errorValue := ensureUser(user); errorValue != nil {
			return errorValue
		}
	}
	for _, user := range state.Users {
		if errorValue := ensureUserGroups(user.Name, user.Groups); errorValue != nil {
			return errorValue
		}
	}
	if commandSucceeds("id", "-u", "blueclaw") {
		if errorValue := ensureUserGroups("blueclaw", groupNames(state.Groups)); errorValue != nil {
			return errorValue
		}
	}
	for _, directory := range state.Directories {
		if errorValue := ensureDirectory(directory); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func groupNames(groups []security.POSIXGroup) []string {
	names := []string{}
	for _, group := range groups {
		names = append(names, group.Name)
	}
	return names
}

func ensureGroup(name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	if commandSucceeds("getent", "group", name) {
		return nil
	}
	return runCommand("groupadd", "--system", name)
}

func ensureUser(user security.POSIXUser) error {
	if commandSucceeds("id", "-u", user.Name) {
		return nil
	}
	return runCommand(
		"useradd",
		"--system",
		"--no-create-home",
		"--home-dir", user.HomePath,
		"--gid", user.GroupName,
		"--shell", "/usr/sbin/nologin",
		user.Name,
	)
}

func ensureUserGroups(userName string, groupNames []string) error {
	for _, groupName := range groupNames {
		if strings.TrimSpace(groupName) == "" {
			continue
		}
		if errorValue := runCommand("usermod", "-a", "-G", groupName, userName); errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func ensureDirectory(directory security.POSIXDirectory) error {
	if errorValue := runCommand("install", "-d", "-o", directory.Owner, "-g", directory.Group, "-m", directory.ModeText, directory.Path); errorValue != nil {
		return errorValue
	}
	if !modeTextIncludesSetGID(directory.ModeText) {
		return nil
	}
	if errorValue := runCommand("chmod", "g+s", directory.Path); errorValue != nil {
		return errorValue
	}
	return nil
}

func modeTextIncludesSetGID(modeText string) bool {
	modeValue, errorValue := strconv.ParseUint(strings.TrimSpace(modeText), 8, 32)
	if errorValue != nil {
		return false
	}
	return modeValue&02000 != 0
}

func commandSucceeds(name string, arguments ...string) bool {
	return exec.Command(name, arguments...).Run() == nil
}

func runCommand(name string, arguments ...string) error {
	command := exec.Command(name, arguments...)
	output, errorValue := command.CombinedOutput()
	if errorValue != nil {
		return fmt.Errorf("%s %s: %s: %w", name, strings.Join(arguments, " "), strings.TrimSpace(string(output)), errorValue)
	}
	return nil
}

func parseGroupIDs(document string) ([]int, error) {
	groupIDs := []int{}
	for _, value := range strings.Split(document, ",") {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" {
			continue
		}
		parsedValue, errorValue := strconv.ParseInt(trimmedValue, 10, 32)
		if errorValue != nil {
			return nil, errorValue
		}
		groupIDs = append(groupIDs, int(parsedValue))
	}
	return groupIDs, nil
}

func parseFileMode(value string) (os.FileMode, error) {
	parsedValue, errorValue := strconv.ParseUint(strings.TrimSpace(value), 8, 32)
	return os.FileMode(parsedValue), errorValue
}

func modeBits(mode os.FileMode) os.FileMode {
	return os.FileMode(uint32(mode) & 07777)
}

func exitWithError(errorValue error) {
	fmt.Fprintln(os.Stderr, errorValue)
	os.Exit(1)
}
