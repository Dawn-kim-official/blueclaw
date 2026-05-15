package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"blueclaw/internal/policy"
	"blueclaw/internal/security"
)

func main() {
	if len(os.Args) < 2 {
		exitWithError(errors.New("blueclaw-posix-helper requires sync or exec"))
	}

	var errorValue error
	switch os.Args[1] {
	case "sync":
		errorValue = runSync(os.Args[2:])
	case "exec":
		errorValue = runExec(os.Args[2:])
	default:
		errorValue = fmt.Errorf("unsupported command %q", os.Args[1])
	}
	if errorValue != nil {
		exitWithError(errorValue)
	}
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
	if errorValue := os.Chdir(*workingDirectoryPath); errorValue != nil {
		return errorValue
	}
	if errorValue := syscall.Setgroups(groupIDs); errorValue != nil {
		return errorValue
	}
	if errorValue := syscall.Setgid(int(*groupID)); errorValue != nil {
		return errorValue
	}
	if errorValue := syscall.Setuid(int(*userID)); errorValue != nil {
		return errorValue
	}
	return syscall.Exec(executableArguments[0], executableArguments, os.Environ())
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

func exitWithError(errorValue error) {
	fmt.Fprintln(os.Stderr, errorValue)
	os.Exit(1)
}
