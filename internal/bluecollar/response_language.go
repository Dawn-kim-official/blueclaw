package bluecollar

func responseLanguageInstruction(responseLanguage string) string {
	switch ResolveResponseLanguage(responseLanguage) {
	case ResponseLanguageEnglish:
		return "Write every user-facing reply, approval question, and recovery message in English. Do not put emoji in message text unless the user explicitly asks for emoji; use message reactions for lightweight acknowledgement."
	default:
		return "Write every user-facing reply, approval question, and recovery message in Korean. Do not put emoji in message text unless the user explicitly asks for emoji; use message reactions for lightweight acknowledgement."
	}
}
