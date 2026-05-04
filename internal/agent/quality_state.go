package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type QualityState struct {
	Passed              bool                       `json:"passed"`
	RecommendedChecks   []string                   `json:"recommendedChecks,omitempty"`
	Warnings            []string                   `json:"warnings,omitempty"`
	RecommendedFixes    []string                   `json:"recommendedFixes,omitempty"`
	CheckedArtifacts    []QualityCheckedArtifact   `json:"checkedArtifacts,omitempty"`
	IgnoredCheckNames   []string                   `json:"ignoredCheckNames,omitempty"`
	ObservedCheckStatus []QualityObservedCheckItem `json:"observedCheckStatus,omitempty"`
	RenderReview        *QualityRenderReview       `json:"renderReview,omitempty"`
}

type QualityCheckedArtifact struct {
	Filename   string `json:"filename,omitempty"`
	Suffix     string `json:"suffix,omitempty"`
	PageCount  int    `json:"pageCount,omitempty"`
	SlideCount int    `json:"slideCount,omitempty"`
}

type QualityObservedCheckItem struct {
	CheckName string `json:"checkName"`
	Passed    bool   `json:"passed"`
	Message   string `json:"message,omitempty"`
}

type QualityRenderReview struct {
	Passed              bool              `json:"passed"`
	SlideCount          int               `json:"slideCount"`
	ContactSheets       []string          `json:"contactSheets,omitempty"`
	Design              map[string]string `json:"design,omitempty"`
	WarningSlideNumbers []int             `json:"warningSlideNumbers,omitempty"`
}

type slideRenderReviewReport struct {
	Passed        bool                         `json:"passed"`
	SlideCount    int                          `json:"slideCount"`
	Design        map[string]string            `json:"design"`
	ContactSheets []slideRenderContactSheet    `json:"contactSheets"`
	Slides        []slideRenderReviewSlideItem `json:"slides"`
}

type slideRenderContactSheet struct {
	Filename     string `json:"filename"`
	SlideNumbers []int  `json:"slideNumbers"`
}

type slideRenderReviewSlideItem struct {
	Index    int                    `json:"index"`
	Filename string                 `json:"filename"`
	Passed   bool                   `json:"passed"`
	Checks   slideRenderReviewCheck `json:"checks"`
	Warnings []string               `json:"warnings"`
}

type slideRenderReviewCheck struct {
	Nonblank     bool `json:"nonblank"`
	SafeMargin   bool `json:"safeMargin"`
	EdgeOverflow bool `json:"edgeOverflow"`
}

func buildQualityState(request AgentTurnRequest, observations []turnObservation, validityState ValidityState) QualityState {
	checks := normalizedQualityChecks(request.QualityRecommendedChecks)
	renderReview, hasRenderReview := loadSlideRenderReview(validityState)
	state := QualityState{
		Passed:            true,
		RecommendedChecks: checks,
		CheckedArtifacts:  qualityCheckedArtifacts(validityState.CheckedArtifacts),
		RenderReview:      summarizeSlideRenderReview(renderReview, hasRenderReview),
	}
	for _, check := range checks {
		state = applyQualityCheck(state, check, observations, validityState, renderReview, hasRenderReview)
	}
	if len(state.Warnings) > 0 {
		state.Passed = false
	}
	return state
}

func normalizedQualityChecks(checks []string) []string {
	normalizedChecks := []string{}
	seenCheck := map[string]bool{}
	for _, check := range checks {
		normalizedCheck := strings.ToLower(strings.TrimSpace(check))
		if normalizedCheck == "" || seenCheck[normalizedCheck] {
			continue
		}
		seenCheck[normalizedCheck] = true
		normalizedChecks = append(normalizedChecks, normalizedCheck)
	}
	return normalizedChecks
}

func applyQualityCheck(state QualityState, check string, observations []turnObservation, validityState ValidityState, renderReview slideRenderReviewReport, hasRenderReview bool) QualityState {
	switch check {
	case "parse_pptx":
		return applyPPTXQualityCheck(state, validityState)
	case "pdf_page_count":
		return applyPDFQualityCheck(state, validityState)
	case "render_nonblank":
		return applyRenderNonblankQualityCheck(state, validityState, renderReview, hasRenderReview)
	case "slide_render_images":
		return applySlideRenderImageQualityCheck(state, renderReview, hasRenderReview)
	case "max_text_overflow", "safe_margin":
		return applySlideMarginQualityCheck(state, check, renderReview, hasRenderReview)
	case "slide_count_min":
		return applySlideCountQualityCheck(state, validityState)
	case "forbidden_reply_fragments":
		return applyForbiddenFragmentQualityCheck(state, observations)
	case "marp_build_log_success":
		return applyMarpBuildLogQualityCheck(state, observations)
	default:
		state.IgnoredCheckNames = append(state.IgnoredCheckNames, check)
		return state
	}
}

func applyPPTXQualityCheck(state QualityState, validityState ValidityState) QualityState {
	for _, artifact := range validityState.CheckedArtifacts {
		if artifact.Suffix == ".pptx" && artifact.Passed {
			return appendQualityObservation(state, "parse_pptx", true, "")
		}
	}
	return appendQualityWarning(state, "parse_pptx", "No valid PPTX artifact was available for quality review.", "Regenerate the deck and ensure the PPTX package opens successfully.")
}

func applyPDFQualityCheck(state QualityState, validityState ValidityState) QualityState {
	hasValidPDF := false
	for _, artifact := range validityState.CheckedArtifacts {
		if artifact.Suffix == ".pdf" && artifact.PageCount > 0 {
			return appendQualityObservation(state, "pdf_page_count", true, "")
		}
		if artifact.Suffix == ".pdf" && artifact.Passed {
			hasValidPDF = true
		}
	}
	if hasValidPDF && maxSlideCount(validityState.CheckedArtifacts) > 0 {
		return appendQualityObservation(state, "pdf_page_count", true, "PDF page count inferred from matching deck slide count.")
	}
	return appendQualityWarning(state, "pdf_page_count", "No PDF artifact with detectable pages was available for quality review.", "Regenerate the PDF export and verify it has at least one page.")
}

func applyRenderNonblankQualityCheck(state QualityState, validityState ValidityState, renderReview slideRenderReviewReport, hasRenderReview bool) QualityState {
	if hasRenderReview {
		blankSlideNumbers := slideNumbersMatching(renderReview, func(slide slideRenderReviewSlideItem) bool {
			return !slide.Checks.Nonblank
		})
		if renderReview.SlideCount > 0 && len(blankSlideNumbers) == 0 {
			return appendQualityObservation(state, "render_nonblank", true, "Slide render review checked "+strconv.Itoa(renderReview.SlideCount)+" PNGs.")
		}
		return appendQualityWarning(state, "render_nonblank", "Blank slide render risk on slides: "+joinNumbers(blankSlideNumbers)+".", "Regenerate slide renders and fix blank or failed slides before delivery.")
	}
	hasRenderableArtifact := false
	for _, artifact := range validityState.CheckedArtifacts {
		if (artifact.Suffix == ".pdf" || artifact.Suffix == ".html") && artifact.Passed && artifact.SizeBytes > 0 {
			hasRenderableArtifact = true
		}
	}
	if hasRenderableArtifact {
		return appendQualityObservation(state, "render_nonblank", true, "")
	}
	return appendQualityWarning(state, "render_nonblank", "No nonblank renderable PDF or HTML artifact was available.", "Regenerate renderable artifacts before delivery.")
}

func applySlideRenderImageQualityCheck(state QualityState, renderReview slideRenderReviewReport, hasRenderReview bool) QualityState {
	if hasRenderReview && renderReview.SlideCount > 0 && len(renderReview.ContactSheets) > 0 {
		return appendQualityObservation(state, "slide_render_images", true, "Rendered "+strconv.Itoa(renderReview.SlideCount)+" slide PNGs into "+strconv.Itoa(len(renderReview.ContactSheets))+" contact sheet(s).")
	}
	return appendQualityWarning(state, "slide_render_images", "No slide render contact sheet was available for review.", "Run the bundled build script so it produces review PNGs, contact sheets, and slide-review.json.")
}

func applySlideMarginQualityCheck(state QualityState, check string, renderReview slideRenderReviewReport, hasRenderReview bool) QualityState {
	if !hasRenderReview {
		return appendQualityWarning(state, check, "No slide render review was available for margin and overflow checks.", "Run the bundled render review before final delivery.")
	}
	warningSlideNumbers := slideNumbersMatching(renderReview, func(slide slideRenderReviewSlideItem) bool {
		return !slide.Checks.SafeMargin || !slide.Checks.EdgeOverflow
	})
	if len(warningSlideNumbers) == 0 && renderReview.SlideCount > 0 {
		return appendQualityObservation(state, check, true, "Slide render review found no edge overflow or unsafe margins.")
	}
	return appendQualityWarning(state, check, "Margin or overflow risk on slides: "+joinNumbers(warningSlideNumbers)+".", "Reduce text density, font size, or visual width on the flagged slides.")
}

func applySlideCountQualityCheck(state QualityState, validityState ValidityState) QualityState {
	for _, artifact := range validityState.CheckedArtifacts {
		if artifact.Suffix == ".pptx" && artifact.SlideCount >= 2 {
			return appendQualityObservation(state, "slide_count_min", true, "")
		}
	}
	return appendQualityWarning(state, "slide_count_min", "The deck has fewer than two detectable slides.", "Add enough slides for the requested presentation.")
}

func applyForbiddenFragmentQualityCheck(state QualityState, observations []turnObservation) QualityState {
	forbiddenFragments := []string{"ppt 못", "직접 생성할 수", "자격 증명", "credentials"}
	for _, observation := range observations {
		content := strings.ToLower(observation.Content)
		for _, fragment := range forbiddenFragments {
			if strings.Contains(content, strings.ToLower(fragment)) {
				return appendQualityWarning(state, "forbidden_reply_fragments", "A discouraged failure phrase appeared during the run.", "Avoid claiming local artifact generation is impossible when local tools are available.")
			}
		}
	}
	return appendQualityObservation(state, "forbidden_reply_fragments", true, "")
}

func applyMarpBuildLogQualityCheck(state QualityState, observations []turnObservation) QualityState {
	for _, observation := range observations {
		if observation.Tool != "terminal.run" || observation.IsError {
			continue
		}
		if strings.Contains(observation.Content, "Building HTML + PPTX + PDF") || strings.Contains(observation.Summary, "Building HTML + PPTX + PDF") {
			return appendQualityObservation(state, "marp_build_log_success", true, "")
		}
	}
	return appendQualityWarning(state, "marp_build_log_success", "No Marp build success log was observed.", "Run the bundled build script and verify it produced HTML, PPTX, PDF, and notes.")
}

func appendQualityObservation(state QualityState, check string, passed bool, message string) QualityState {
	state.ObservedCheckStatus = append(state.ObservedCheckStatus, QualityObservedCheckItem{
		CheckName: check,
		Passed:    passed,
		Message:   message,
	})
	return state
}

func appendQualityWarning(state QualityState, check string, warning string, recommendedFix string) QualityState {
	state.Warnings = append(state.Warnings, warning)
	state.RecommendedFixes = append(state.RecommendedFixes, recommendedFix)
	return appendQualityObservation(state, check, false, warning)
}

func qualityCheckedArtifacts(artifacts []ArtifactValidity) []QualityCheckedArtifact {
	checkedArtifacts := []QualityCheckedArtifact{}
	for _, artifact := range artifacts {
		checkedArtifacts = append(checkedArtifacts, QualityCheckedArtifact{
			Filename:   artifact.Filename,
			Suffix:     artifact.Suffix,
			PageCount:  artifact.PageCount,
			SlideCount: artifact.SlideCount,
		})
	}
	return checkedArtifacts
}

func loadSlideRenderReview(validityState ValidityState) (slideRenderReviewReport, bool) {
	for _, artifact := range validityState.CheckedArtifacts {
		if strings.TrimSpace(artifact.path) == "" {
			continue
		}
		path := filepath.Join(filepath.Dir(artifact.path), "review", "slide-review.json")
		content, errorValue := os.ReadFile(path)
		if errorValue != nil {
			continue
		}
		var report slideRenderReviewReport
		if errorValue := json.Unmarshal(content, &report); errorValue != nil {
			continue
		}
		return report, true
	}
	return slideRenderReviewReport{}, false
}

func summarizeSlideRenderReview(report slideRenderReviewReport, hasReport bool) *QualityRenderReview {
	if !hasReport {
		return nil
	}
	contactSheets := []string{}
	for _, contactSheet := range report.ContactSheets {
		if strings.TrimSpace(contactSheet.Filename) != "" {
			contactSheets = append(contactSheets, contactSheet.Filename)
		}
	}
	return &QualityRenderReview{
		Passed:              report.Passed,
		SlideCount:          report.SlideCount,
		ContactSheets:       contactSheets,
		Design:              report.Design,
		WarningSlideNumbers: slideNumbersMatching(report, func(slide slideRenderReviewSlideItem) bool { return !slide.Passed }),
	}
}

func slideNumbersMatching(report slideRenderReviewReport, matcher func(slide slideRenderReviewSlideItem) bool) []int {
	slideNumbers := []int{}
	for _, slide := range report.Slides {
		if matcher(slide) {
			slideNumbers = append(slideNumbers, slide.Index)
		}
	}
	return slideNumbers
}

func joinNumbers(values []int) string {
	if len(values) == 0 {
		return "none"
	}
	parts := []string{}
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, ", ")
}

func maxSlideCount(artifacts []ArtifactValidity) int {
	maximumSlideCount := 0
	for _, artifact := range artifacts {
		if artifact.SlideCount > maximumSlideCount {
			maximumSlideCount = artifact.SlideCount
		}
	}
	return maximumSlideCount
}
