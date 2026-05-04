package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSimpleSlidesCreatorPreservesCapabilitiesSignals(t *testing.T) {
	cases := []struct {
		name  string
		brief string
	}{
		{
			name: "korean",
			brief: strings.Join([]string{
				"original_user_request: 너 뭐 할 수 있는지 8장 피피티 만들어서 보내줘봐",
				"audience: 동하 님",
				"desired_tone: 전문적이고 선명한",
				"slide_topic: 김인턴이 할 수 있는 일",
			}, "\n"),
		},
		{
			name: "english",
			brief: strings.Join([]string{
				"original_user_request: Create a slide deck explaining what I can do as a teammate.",
				"audience: Dongha",
				"desired_tone: Professional",
				"slide_topic: Intern Kim Capability Overview",
			}, "\n"),
		},
		{
			name: "quoted",
			brief: strings.Join([]string{
				"original_user_request: 'Create a slide deck explaining what I can do as a teammate.'",
				"audience: Dongha",
				"desired_tone: Professional",
				"slide_topic: Intern Kim Capability Overview",
			}, "\n"),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			workingDirectoryPath := t.TempDir()
			if errorValue := os.WriteFile(filepath.Join(workingDirectoryPath, "brief.md"), []byte(testCase.brief), 0600); errorValue != nil {
				t.Fatal(errorValue)
			}
			runSimpleSlidesCreator(t, workingDirectoryPath, "capability-deck")
			presentation := readTestFile(t, filepath.Join(workingDirectoryPath, "presentation.md"))

			for _, fragment := range []string{"InternKim capability deck", "Paperlogy", "Freesentation", "--background"} {
				if !strings.Contains(presentation, fragment) {
					t.Fatalf("expected presentation to contain %q:\n%s", fragment, presentation)
				}
			}
			if strings.Contains(presentation, "Presentation brief") {
				t.Fatalf("expected capabilities presentation, got generic presentation:\n%s", presentation)
			}
		})
	}
}

func TestSimpleSlidesCreatorEnforcesRequestedSlideCount(t *testing.T) {
	workingDirectoryPath := t.TempDir()
	brief := strings.Join([]string{
		"original_user_request: 너 뭐 할 수 있는지 8장 피피티 만들어서 보내줘봐",
		"audience: 동하 님",
		"desired_tone: 전문적이고 선명한",
		"slide_topic: 김인턴이 할 수 있는 일",
	}, "\n")
	if errorValue := os.WriteFile(filepath.Join(workingDirectoryPath, "brief.md"), []byte(brief), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	runSimpleSlidesCreator(t, workingDirectoryPath, "capability-deck")
	presentation := readTestFile(t, filepath.Join(workingDirectoryPath, "presentation.md"))

	if slideCount := countPresentationSlides(presentation); slideCount != 8 {
		t.Fatalf("expected 8 slides, got %d:\n%s", slideCount, presentation)
	}
}

func TestSimpleSlidesCreatorFailsUnsupportedSlideCount(t *testing.T) {
	workingDirectoryPath := t.TempDir()
	brief := "original_user_request: 너 뭐 할 수 있는지 12장 피피티 만들어서 보내줘봐"
	if errorValue := os.WriteFile(filepath.Join(workingDirectoryPath, "brief.md"), []byte(brief), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
	command := exec.Command("python3", simpleSlidesCreatorPath(), "--slug", "capability-deck", "--brief", "brief.md", "--no-build")
	command.Dir = workingDirectoryPath
	output, errorValue := command.CombinedOutput()
	if errorValue == nil {
		t.Fatalf("expected unsupported slide count to fail, output:\n%s", string(output))
	}
	if !strings.Contains(string(output), "requested 12 slides") {
		t.Fatalf("expected slide count failure, got:\n%s", string(output))
	}
}

func runSimpleSlidesCreator(t *testing.T, workingDirectoryPath string, slug string) {
	t.Helper()
	command := exec.Command("python3", simpleSlidesCreatorPath(), "--slug", slug, "--brief", "brief.md", "--no-build")
	command.Dir = workingDirectoryPath
	output, errorValue := command.CombinedOutput()
	if errorValue != nil {
		t.Fatalf("create_deck.py failed: %v\n%s", errorValue, string(output))
	}
}

func simpleSlidesCreatorPath() string {
	workingDirectoryPath, errorValue := os.Getwd()
	if errorValue != nil {
		return filepath.Clean("../../../../assets/blueclaw-workspace/skills/simple-slides/scripts/create_deck.py")
	}
	return filepath.Clean(filepath.Join(workingDirectoryPath, "../../../../assets/blueclaw-workspace/skills/simple-slides/scripts/create_deck.py"))
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return string(content)
}

func countPresentationSlides(presentation string) int {
	parts := strings.SplitN(presentation, "---", 3)
	body := strings.TrimSpace(presentation)
	if len(parts) == 3 {
		body = strings.TrimSpace(parts[2])
	}
	if body == "" {
		return 0
	}
	return len(strings.Split(body, "\n---\n"))
}
