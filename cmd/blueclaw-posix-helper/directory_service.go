package main

import (
	"strconv"
	"strings"
)

const macOSServiceAccountShell = "/usr/bin/false"

func userRecordPath(userName string) string {
	return "/Users/" + userName
}

func groupRecordPath(groupName string) string {
	return "/Groups/" + groupName
}

func createGroupCommands(name string, groupID uint32) [][]string {
	record := groupRecordPath(name)
	return [][]string{
		{".", "-create", record},
		{".", "-create", record, "RealName", name},
		{".", "-create", record, "PrimaryGroupID", formatID(groupID)},
	}
}

func createUserCommands(name string, homePath string, userID uint32, groupID uint32) [][]string {
	record := userRecordPath(name)
	return [][]string{
		{".", "-create", record},
		{".", "-create", record, "RealName", name},
		{".", "-create", record, "UserShell", macOSServiceAccountShell},
		{".", "-create", record, "NFSHomeDirectory", homePath},
		{".", "-create", record, "IsHidden", "1"},
		{".", "-create", record, "PrimaryGroupID", formatID(groupID)},
		{".", "-create", record, "UniqueID", formatID(userID)},
	}
}

func parseDirectoryServiceList(output string) []systemIdentity {
	identities := []systemIdentity{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		identityID, errorValue := strconv.ParseUint(fields[len(fields)-1], 10, 32)
		if errorValue != nil {
			continue
		}
		identities = append(identities, systemIdentity{
			name:       strings.Join(fields[:len(fields)-1], " "),
			identityID: uint32(identityID),
		})
	}
	return identities
}

func parseDirectoryServiceValue(output string) string {
	firstLine, remainingLines, _ := strings.Cut(output, "\n")
	separatorIndex := strings.LastIndex(firstLine, ":")
	if separatorIndex < 0 {
		return strings.TrimSpace(output)
	}
	return strings.TrimSpace(firstLine[separatorIndex+1:] + " " + remainingLines)
}
