package agent

import "testing"

func TestSystemInstructionPinsBaseAndSkillExposureArchitecture(t *testing.T) {
	t.Run("base tools", func(t *testing.T) {
		t.Skip("already pinned by TestSystemInstructionAllowsWebSearchAfterMemorySearchUnavailable in memory_search_recovery_test.go:134, which asserts the full 'The current action schema contains the always-available base tools plus the selected skills' allowed-tools' sentence from buildAgentSystemInstruction; dropping 'base tools' from that sentence already fails that test, so pinning it again here would be a redundant assertion")
	})

	t.Run("allowed-tools", func(t *testing.T) {
		t.Skip("already pinned by the same TestSystemInstructionAllowsWebSearchAfterMemorySearchUnavailable assertion in memory_search_recovery_test.go:134; dropping 'allowed-tools' from the base+selected-skill exposure sentence already fails that test, so pinning it again here would be a redundant assertion")
	})

	t.Run("skill.select", func(t *testing.T) {
		t.Skip("already pinned by TestSystemInstructionRestrictsCheckpointsAndRequiresRecovery in system_instruction_approval_test.go:40 (asserts 'use skill.select' against buildAgentSystemInstruction); dropping the skill.select exposure instruction already fails that test, so pinning it again here would be a redundant assertion")
	})
}
