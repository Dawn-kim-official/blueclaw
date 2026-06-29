package agentruntime

import "testing"

func TestAmbientCaptureAllowedToolNamesClampsToTaskAndCalendar(t *testing.T) {
	allowed := map[string]bool{}
	for _, toolName := range ambientCaptureAllowedToolNames() {
		allowed[toolName] = true
	}

	for _, requiredToolName := range []string{"task.add", "task.update", "calendar.add", "ask.input"} {
		if !allowed[requiredToolName] {
			t.Fatalf("ambient capture palette must allow %q", requiredToolName)
		}
	}

	for _, forbiddenToolName := range []string{"terminal.run", "web.fetch", "file.write", "task.delete", "calendar.delete", "message.send"} {
		if allowed[forbiddenToolName] {
			t.Fatalf("ambient capture palette must not allow %q", forbiddenToolName)
		}
	}
}
