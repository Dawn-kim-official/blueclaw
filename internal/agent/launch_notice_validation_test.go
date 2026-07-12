package agent

import "testing"

func TestLaunchNoticeStatesTimeEstimate(t *testing.T) {
	testCases := []struct {
		name         string
		launchNotice string
		wantsMatch   bool
	}{
		{
			name:         "production regression: mixed Korean sentence with 약 N분",
			launchNotice: "요청하신 IR 덱 디자인 개선 및 내용 보충 작업을 시작했습니다. 약 10분 정도 소요될 수 있습니다.",
			wantsMatch:   true,
		},
		{
			name:         "digit plus 분 with 소요",
			launchNotice: "5분 정도 소요됩니다.",
			wantsMatch:   true,
		},
		{
			name:         "digit plus 시간 with 약 prefix",
			launchNotice: "약 2시간 걸립니다.",
			wantsMatch:   true,
		},
		{
			name:         "English minutes",
			launchNotice: "This takes about 15 minutes.",
			wantsMatch:   true,
		},
		{
			name:         "English hour singular",
			launchNotice: "It should take roughly 1 hour.",
			wantsMatch:   true,
		},
		{
			name:         "English abbreviated min",
			launchNotice: "Back in about 10 min.",
			wantsMatch:   true,
		},
		{
			name:         "native Korean numeral hour with space",
			launchNotice: "대략 한 시간 정도 걸릴 것 같습니다.",
			wantsMatch:   true,
		},
		{
			name:         "native Korean numeral hour without space",
			launchNotice: "대략 한시간 정도 걸릴 것 같습니다.",
			wantsMatch:   true,
		},
		{
			name:         "plain start acknowledgement",
			launchNotice: "요청하신 작업을 시작했습니다.",
			wantsMatch:   false,
		},
		{
			name:         "plain progress acknowledgement",
			launchNotice: "차근차근 진행하겠습니다.",
			wantsMatch:   false,
		},
		{
			name:         "bare 빨리 must not trip",
			launchNotice: "빨리 처리해드리겠습니다.",
			wantsMatch:   false,
		},
		{
			name:         "bare 곧 must not trip",
			launchNotice: "곧 완료됩니다.",
			wantsMatch:   false,
		},
		{
			name:         "task subject mentioning a clock time, not a duration",
			launchNotice: "오전 10시에 있을 회의 준비를 시작했습니다.",
			wantsMatch:   false,
		},
		{
			name:         "empty notice",
			launchNotice: "",
			wantsMatch:   false,
		},
		{
			name:         "whitespace only notice",
			launchNotice: "   ",
			wantsMatch:   false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			gotMatch := launchNoticeStatesTimeEstimate(testCase.launchNotice)
			if gotMatch != testCase.wantsMatch {
				t.Fatalf("launchNoticeStatesTimeEstimate(%q) = %v, want %v", testCase.launchNotice, gotMatch, testCase.wantsMatch)
			}
		})
	}
}
