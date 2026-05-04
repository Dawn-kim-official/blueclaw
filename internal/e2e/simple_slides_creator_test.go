package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSimpleSlidesCreatorFailsWithoutBrief(t *testing.T) {
	workingDirectoryPath := t.TempDir()
	writeSimpleSlidesDesignAndPresentation(t, workingDirectoryPath, hermesPresentation())

	command := exec.Command("python3", simpleSlidesCreatorPath(), "--slug", "hermes-analysis", "--brief", "brief.md", "--no-build")
	command.Dir = workingDirectoryPath
	output, errorValue := command.CombinedOutput()
	if errorValue == nil {
		t.Fatalf("expected missing brief to fail, output:\n%s", string(output))
	}
	if !strings.Contains(string(output), "brief file is required") {
		t.Fatalf("expected missing brief failure, got:\n%s", string(output))
	}
}

func TestSimpleSlidesCreatorFailsWithoutDeckSpec(t *testing.T) {
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

	command := exec.Command("python3", simpleSlidesCreatorPath(), "--slug", "hermes-analysis", "--brief", "brief.md", "--no-build")
	command.Dir = workingDirectoryPath
	output, errorValue := command.CombinedOutput()
	if errorValue == nil {
		t.Fatalf("expected missing deck_spec to fail, output:\n%s", string(output))
	}
	if !strings.Contains(string(output), "deck_spec is required") {
		t.Fatalf("expected deck_spec failure, got:\n%s", string(output))
	}
}

func TestSimpleSlidesCreatorValidatesHermesDeckSpec(t *testing.T) {
	workingDirectoryPath := t.TempDir()
	writeSimpleSlidesDesignAndPresentation(t, workingDirectoryPath, hermesPresentation())
	writeTestFile(t, filepath.Join(workingDirectoryPath, "brief.md"), hermesBrief())

	runSimpleSlidesCreator(t, workingDirectoryPath, "hermes-analysis")

	intentManifest := readTestFile(t, filepath.Join(workingDirectoryPath, "hermes-analysis-intent.json"))
	presentation := readTestFile(t, filepath.Join(workingDirectoryPath, "presentation.md"))
	if !strings.Contains(intentManifest, `"requested_formats": [`) || !strings.Contains(intentManifest, `"html"`) {
		t.Fatalf("expected html requested format manifest, got:\n%s", intentManifest)
	}
	if slideCount := countPresentationSlides(presentation); slideCount != 6 {
		t.Fatalf("expected 6 slides, got %d:\n%s", slideCount, presentation)
	}
	for _, forbidden := range []string{"InternKim capability deck", "김인턴이 할 수 있는 일"} {
		if strings.Contains(presentation, forbidden) {
			t.Fatalf("presentation contains forbidden sample token %q:\n%s", forbidden, presentation)
		}
	}
}

func TestSimpleSlidesCreatorRejectsMismatchedPresentation(t *testing.T) {
	workingDirectoryPath := t.TempDir()
	writeSimpleSlidesDesignAndPresentation(t, workingDirectoryPath, capabilitiesPresentation())
	writeTestFile(t, filepath.Join(workingDirectoryPath, "brief.md"), hermesBrief())

	command := exec.Command("python3", simpleSlidesCreatorPath(), "--slug", "hermes-analysis", "--brief", "brief.md", "--no-build")
	command.Dir = workingDirectoryPath
	output, errorValue := command.CombinedOutput()
	if errorValue == nil {
		t.Fatalf("expected mismatched presentation to fail, output:\n%s", string(output))
	}
	if !strings.Contains(string(output), "sample token") {
		t.Fatalf("expected sample-token failure, got:\n%s", string(output))
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
		`{"slides":[` +
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
		"<div class=\"eyebrow\">InternKim capability deck</div>",
		"# 김인턴이 할 수 있는 일",
		"---",
		"## 김인턴이 끝까지 처리하는 일",
		"자료를 만듭니다.",
		"---",
		"## 마무리",
		"김인턴이 할 수 있는 일",
	}, "\n")
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
