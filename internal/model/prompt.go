package model

// BuildToDoPrompt combines a user prompt with note content into the final task prompt.
// If the user entered a prompt, it becomes the primary instruction with the note as context.
// If no user prompt, the note content is used directly (backwards compatible).
// The <context> XML-style tags are a Claude convention for structured context sections.
func BuildToDoPrompt(userPrompt, noteContent string) string {
	if userPrompt == "" {
		return noteContent
	}
	if noteContent == "" {
		return userPrompt
	}
	return userPrompt + "\n\n<context>\n" + noteContent + "\n</context>"
}
