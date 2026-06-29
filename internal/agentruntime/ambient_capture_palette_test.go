package agentruntime

import "testing"

func TestAmbientCaptureAllowedToolNamesClampsToTaskAndCalendar(t *testing.T) {
	allowed := map[string]bool{}
	for _, toolName := range ambientCaptureAllowedToolNames() {
		allowed[toolName] = true
	}

	for _, requiredToolName := range []string{"flow.task.add", "flow.task.update", "calendar.event.add", "ask.input"} {
		if !allowed[requiredToolName] {
			t.Fatalf("ambient capture palette must allow %q", requiredToolName)
		}
	}

	for _, forbiddenToolName := range []string{"terminal.run", "web.fetch", "file.write", "flow.task.delete", "calendar.event.delete", "platform.message.send"} {
		if allowed[forbiddenToolName] {
			t.Fatalf("ambient capture palette must not allow %q", forbiddenToolName)
		}
	}
}
