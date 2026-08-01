package agentruntime

import "testing"

func TestAmbientCaptureAllowedToolNamesClampsToTaskAndCalendar(t *testing.T) {
	allowed := map[string]bool{}
	for _, toolName := range ambientCaptureAllowedToolNames() {
		allowed[toolName] = true
	}

	for _, requiredToolName := range []string{"task_add", "task_update", "calendar_add", "ask_input"} {
		if !allowed[requiredToolName] {
			t.Fatalf("ambient capture palette must allow %q", requiredToolName)
		}
	}

	for _, forbiddenToolName := range []string{"terminal_run", "web_fetch", "file_write", "task_delete", "calendar_delete", "message_send"} {
		if allowed[forbiddenToolName] {
			t.Fatalf("ambient capture palette must not allow %q", forbiddenToolName)
		}
	}
}
