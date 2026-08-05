package main

import (
	"os/exec"
	"strings"
	"testing"
)

const bundledHarnessPackage = "github.com/yeomyeonggeori/bluecollar/loop"

func TestTheNoBundledHarnessBuildLeavesTheAgentLoopOut(testInstance *testing.T) {
	if linkedPackages(testInstance, "nobundledharness")[bundledHarnessPackage] {
		testInstance.Fatalf("a -tags nobundledharness build still links %s; something imports the loop outside cmd/blueclaw/bundled_harness.go", bundledHarnessPackage)
	}
}

func TestTheDefaultBuildStillShipsTheBundledHarness(testInstance *testing.T) {
	if !linkedPackages(testInstance)[bundledHarnessPackage] {
		testInstance.Fatalf("the default build no longer links %s, so the bundled harness would be unreachable", bundledHarnessPackage)
	}
}

func linkedPackages(testInstance *testing.T, buildTags ...string) map[string]bool {
	testInstance.Helper()
	arguments := []string{"list", "-deps"}
	for _, buildTag := range buildTags {
		arguments = append(arguments, "-tags", buildTag)
	}
	output, errorValue := exec.Command("go", append(arguments, ".")...).Output()
	if errorValue != nil {
		testInstance.Fatalf("go %s: %v", strings.Join(arguments, " "), errorValue)
	}
	linked := map[string]bool{}
	for _, packagePath := range strings.Fields(string(output)) {
		linked[packagePath] = true
	}
	return linked
}
