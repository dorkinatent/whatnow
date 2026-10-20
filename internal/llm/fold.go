package llm

import (
	"fmt"
	"strings"
)

// flatSystem joins a system prompt's blocks, for backends that take it as one string. Their
// cache marks are lost.
func flatSystem(blocks []Block) string {
	texts := make([]string, len(blocks))
	for i, b := range blocks {
		texts[i] = b.Text
	}
	return strings.Join(texts, "\n\n")
}

// transcript folds a chat into one prompt, for backends that can't be handed earlier turns: the
// earlier messages as a tagged record, then the last message, which is the user's.
func transcript(msgs []Message) string {
	if len(msgs) == 0 {
		return ""
	}
	last := msgs[len(msgs)-1].Text
	if len(msgs) == 1 {
		return last
	}
	var b strings.Builder
	b.WriteString("Earlier in this conversation:\n\n")
	for _, m := range msgs[:len(msgs)-1] {
		fmt.Fprintf(&b, "<%s>\n%s\n</%s>\n\n", m.Role, m.Text, m.Role)
	}
	b.WriteString("The user's new message:\n\n")
	b.WriteString(last)
	return b.String()
}

func joinText(a, b string) string {
	if a == "" || b == "" {
		return a + b
	}
	return a + "\n\n" + b
}
