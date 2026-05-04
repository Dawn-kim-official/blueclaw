package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSimpleSlidesCreatorBuildsExistingPresentationWithoutBrief(t *testing.T) {
	workingDirectoryPath := t.TempDir()
	writeSimpleSlidesDesignAndPresentation(t, workingDirectoryPath, hermesPresentation())

	runSimpleSlidesCreator(t, workingDirectoryPath, "hermes-analysis")

	if _, errorValue := os.Stat(filepath.Join(workingDirectoryPath, "build.sh")); errorValue != nil {
		t.Fatalf("expected runtime build script to be copied: %v", errorValue)
	}
}

func TestSimpleSlidesCreatorBuildsExistingPresentationWithoutDeckSpec(t *testing.T) {
	workingDirectoryPath := t.TempDir()
	writeSimpleSlidesDesignAndPresentation(t, workingDirectoryPath, hermesPresentation())
	writeTestFile(t, filepath.Join(workingDirectoryPath, "brief.md"), strings.Join([]string{
		"original_user_request: Hermes Agent 장단점 분석 ppt를 6장으로 만들어줘. html만 주면 돼",
		"topic: Hermes Agent",
		"slide_intent: 장단점 분석",
		"requested_slide_count: 6",
		"requested_formats: html",
		"output_slug: hermes-analysis",
	}, "\n"))

	runSimpleSlidesCreator(t, workingDirectoryPath, "hermes-analysis")

	presentation := readTestFile(t, filepath.Join(workingDirectoryPath, "presentation.md"))
	if slideCount := countPresentationSlides(presentation); slideCount != 6 {
		t.Fatalf("expected existing 6-slide presentation to be preserved, got %d:\n%s", slideCount, presentation)
	}
}

func TestSimpleSlidesCreatorCanCreatePresentationFromDeckSpec(t *testing.T) {
	workingDirectoryPath := t.TempDir()
	writeTestFile(t, filepath.Join(workingDirectoryPath, "brief.md"), hermesBrief())

	runSimpleSlidesCreator(t, workingDirectoryPath, "hermes-analysis")

	presentation := readTestFile(t, filepath.Join(workingDirectoryPath, "presentation.md"))
	if slideCount := countPresentationSlides(presentation); slideCount != 6 {
		t.Fatalf("expected 6 slides from deck_spec, got %d:\n%s", slideCount, presentation)
	}
	if !strings.Contains(presentation, "Hermes Agent 분석 프레임") {
		t.Fatalf("expected generated presentation to include deck_spec content:\n%s", presentation)
	}
}

func TestSimpleSlidesCreatorAllowsMismatchedImperfectPresentation(t *testing.T) {
	workingDirectoryPath := t.TempDir()
	writeSimpleSlidesDesignAndPresentation(t, workingDirectoryPath, capabilitiesPresentation())
	writeTestFile(t, filepath.Join(workingDirectoryPath, "brief.md"), hermesBrief())

	runSimpleSlidesCreator(t, workingDirectoryPath, "hermes-analysis")
}

func TestSimpleSlidesCreatorFailsWithoutAnyDeckSource(t *testing.T) {
	workingDirectoryPath := t.TempDir()

	command := exec.Command("python3", simpleSlidesCreatorPath(), "--slug", "hermes-analysis", "--brief", "brief.md", "--no-build")
	command.Dir = workingDirectoryPath
	output, errorValue := command.CombinedOutput()
	if errorValue == nil {
		t.Fatalf("expected missing deck source to fail, output:\n%s", string(output))
	}
	if !strings.Contains(string(output), "presentation.md is missing") {
		t.Fatalf("expected missing presentation guidance, got:\n%s", string(output))
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

func writeSimpleSlidesDesignAndPresentation(t *testing.T, workingDirectoryPath string, presentation string) {
	t.Helper()
	writeTestFile(t, filepath.Join(workingDirectoryPath, "DESIGN.md"), strings.Join([]string{
		"---",
		"colors:",
		"  background: \"#F8FAFC\"",
		"typography:",
		"  display: \"Paperlogy, Freesentation\"",
		"layout:",
		"  canvas: \"16:9\"",
		"---",
		"",
		"# Deck Design",
	}, "\n"))
	writeTestFile(t, filepath.Join(workingDirectoryPath, "presentation.md"), presentation)
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if errorValue := os.MkdirAll(filepath.Dir(path), 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return string(content)
}

func hermesBrief() string {
	return strings.Join([]string{
		"original_user_request: Hermes Agent 장단점 분석 ppt를 6장으로 만들어줘. html만 주면 돼",
		"topic: Hermes Agent",
		"slide_intent: 장단점 분석",
		"requested_slide_count: 6",
		"requested_formats: html",
		"output_slug: hermes-analysis",
		"deck_spec:",
		"```json",
		`{"title":"Hermes Agent 장단점 분석","slides":[` +
			`{"title":"Hermes Agent 분석 프레임","body":["Hermes Agent의 장점과 단점을 실행형 에이전트 관점에서 비교한다."],"speaker_note":"Hermes Agent 장단점 분석의 기준을 먼저 제시합니다."},` +
			`{"title":"Hermes Agent 장점 1","body":["Hermes Agent는 도구 호출과 산출물 생성을 하나의 흐름으로 묶는 장점이 있다."],"speaker_note":"Hermes Agent의 첫 번째 장점은 실행 흐름입니다."},` +
			`{"title":"Hermes Agent 장점 2","body":["Hermes Agent는 evidence 기반 완료 판단으로 반복 업무를 안정화할 수 있다."],"speaker_note":"Hermes Agent의 두 번째 장점은 증거 중심 완료입니다."},` +
			`{"title":"Hermes Agent 단점 1","body":["Hermes Agent는 런타임 계약이 느슨하면 잘못된 파일도 성공처럼 보낼 수 있다."],"speaker_note":"Hermes Agent의 첫 번째 단점은 계약 누락 리스크입니다."},` +
			`{"title":"Hermes Agent 단점 2","body":["Hermes Agent는 장기 작업에서 상태와 타겟이 섞이면 로그 분석이 어려워진다."],"speaker_note":"Hermes Agent의 두 번째 단점은 상태 혼선입니다."},` +
			`{"title":"Hermes Agent 개선 판단","body":["Hermes Agent는 명시적 계약과 outbox evidence가 붙을 때 장점이 단점을 압도한다."],"speaker_note":"Hermes Agent 장단점 분석의 결론을 정리합니다."}` +
			`]}`,
		"```",
	}, "\n")
}

func hermesPresentation() string {
	return strings.Join([]string{
		"---",
		"marp: true",
		"title: Hermes Agent 장단점 분석",
		"---",
		"<!-- design-source: DESIGN.md -->",
		"## Hermes Agent 분석 프레임",
		"Hermes Agent의 장점과 단점을 실행형 에이전트 관점에서 비교한다.",
		"<!-- Hermes Agent 장단점 분석의 기준을 먼저 제시합니다. -->",
		"---",
		"## Hermes Agent 장점 1",
		"Hermes Agent는 도구 호출과 산출물 생성을 하나의 흐름으로 묶는 장점이 있다.",
		"<!-- Hermes Agent의 첫 번째 장점은 실행 흐름입니다. -->",
		"---",
		"## Hermes Agent 장점 2",
		"Hermes Agent는 evidence 기반 완료 판단으로 반복 업무를 안정화할 수 있다.",
		"<!-- Hermes Agent의 두 번째 장점은 증거 중심 완료입니다. -->",
		"---",
		"## Hermes Agent 단점 1",
		"Hermes Agent는 런타임 계약이 느슨하면 잘못된 파일도 성공처럼 보낼 수 있다.",
		"<!-- Hermes Agent의 첫 번째 단점은 계약 누락 리스크입니다. -->",
		"---",
		"## Hermes Agent 단점 2",
		"Hermes Agent는 장기 작업에서 상태와 타겟이 섞이면 로그 분석이 어려워진다.",
		"<!-- Hermes Agent의 두 번째 단점은 상태 혼선입니다. -->",
		"---",
		"## Hermes Agent 개선 판단",
		"Hermes Agent는 명시적 계약과 outbox evidence가 붙을 때 장점이 단점을 압도한다.",
		"<!-- Hermes Agent 장단점 분석의 결론을 정리합니다. -->",
	}, "\n")
}

func capabilitiesPresentation() string {
	return strings.Join([]string{
		"---",
		"marp: true",
		"title: 김인턴이 할 수 있는 일",
		"---",
		"<!-- design-source: DESIGN.md -->",
		"# InternKim capability deck",
		"김인턴이 할 수 있는 일",
	}, "\n")
}

func countPresentationSlides(presentation string) int {
	parts := strings.SplitN(presentation, "---", 3)
	body := presentation
	if len(parts) == 3 && strings.TrimSpace(parts[0]) == "" {
		body = parts[2]
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return 0
	}
	return len(strings.Split(body, "\n---\n"))
}
