package agent

import (
	"strings"
	"testing"
)

func TestCompanyContextRendersIdentityAndSelfUpdateRule(t *testing.T) {
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{
		Company: CompanyContext{
			Name:           "주식회사 여명거리",
			BrandName:      "김인턴",
			Slogan:         "AI 인턴을 모든 회사에",
			Description:    "AI 인턴 서비스를 만드는 스타트업",
			Representative: "김여명",
			Website:        "https://example.test",
		},
	})
	for _, expected := range []string{
		"Our company:",
		"주식회사 여명거리 (브랜드: 김인턴) — AI 인턴을 모든 회사에",
		"대표 김여명",
		"company.info.get",
		"company.metric.record",
	} {
		if !strings.Contains(contextText, expected) {
			t.Fatalf("company context missing %q in:\n%s", expected, contextText)
		}
	}
}

func TestCompanyContextOmittedWhenEmpty(t *testing.T) {
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{})
	if strings.Contains(contextText, "Our company:") {
		t.Fatalf("empty company should not render a company section:\n%s", contextText)
	}
}

func TestAgentKernelCompanyProviderFeedsTurnRequest(t *testing.T) {
	agentKernel := &AgentKernel{}
	agentKernel.UseCompanyProvider(func() CompanyContext {
		return CompanyContext{Name: "주식회사 여명거리"}
	})
	if agentKernel.companyContext().Name != "주식회사 여명거리" {
		t.Fatal("company provider not applied")
	}
	agentKernel = &AgentKernel{}
	if !agentKernel.companyContext().IsEmpty() {
		t.Fatal("missing provider should yield empty company")
	}
}
