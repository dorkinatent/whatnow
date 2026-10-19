package llm

import "testing"

func TestTranscriptFoldsEarlierMessagesAboveTheNewOne(t *testing.T) {
	got := transcript([]Message{
		{Role: RoleUser, Text: "move the dentist"},
		{Role: RoleAssistant, Text: "Moved to Thursday."},
		{Role: RoleUser, Text: "and the RFC?"},
	})
	want := "Earlier in this conversation:\n\n" +
		"<user>\nmove the dentist\n</user>\n\n" +
		"<assistant>\nMoved to Thursday.\n</assistant>\n\n" +
		"The user's new message:\n\nand the RFC?"
	if got != want {
		t.Errorf("transcript =\n%s\nwant\n%s", got, want)
	}
}

func TestTranscriptOfOneMessageIsJustIt(t *testing.T) {
	if got := transcript([]Message{{Role: RoleUser, Text: "hi"}}); got != "hi" {
		t.Errorf("transcript = %q", got)
	}
	if got := transcript(nil); got != "" {
		t.Errorf("empty transcript = %q", got)
	}
}

func TestFlatSystemJoinsBlocks(t *testing.T) {
	got := flatSystem([]Block{{Text: "You plan days.", Cache: true}, {Text: "Today is Monday."}})
	if got != "You plan days.\n\nToday is Monday." {
		t.Errorf("flatSystem = %q", got)
	}
}
