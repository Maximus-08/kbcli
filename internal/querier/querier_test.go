package querier

import (
	"testing"
)

func TestCleanThoughtTags(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "standard thought tags",
			input:    "<thought>This is some thinking process</thought>The actual answer is here.",
			expected: "The actual answer is here.",
		},
		{
			name:     "standard think tags",
			input:    "<think>Thinking deeply...</think>DeepSeek answer here.",
			expected: "DeepSeek answer here.",
		},
		{
			name:     "multiple thinking blocks",
			input:    "<thought>Thought 1</thought>Part 1 <think>Thought 2</think>Part 2",
			expected: "Part 1 Part 2",
		},
		{
			name:     "case insensitivity",
			input:    "<THOUGHT>shouting thoughts</THOUGHT>Answer here.",
			expected: "Answer here.",
		},
		{
			name:     "unclosed thought tag (truncated response)",
			input:    "Preamble <thought>I am thinking forever and ever...",
			expected: "Preamble",
		},
		{
			name:     "unclosed think tag",
			input:    "Starting answer <think>nested logic",
			expected: "Starting answer",
		},
		{
			name:     "tags with attributes",
			input:    `<thought class="reasoning" id="1">Attributes thoughts</thought>Answer.`,
			expected: "Answer.",
		},
		{
			name:     "no tags",
			input:    "Simple plain answer without thoughts.",
			expected: "Simple plain answer without thoughts.",
		},
		{
			name:     "whitespace trimming",
			input:    "   \n <thought>thinking</thought>   Clean Answer \n  ",
			expected: "Clean Answer",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := cleanThoughtTags(tc.input)
			if actual != tc.expected {
				t.Errorf("cleanThoughtTags(%q) = %q; expected %q", tc.input, actual, tc.expected)
			}
		})
	}
}

func TestFormatAnswerForTerminal(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "dirac notation and inline math",
			input:    "The state is $|-\\rangle$ or $|+\\rangle$.",
			expected: "The state is |-⟩ or |+⟩.",
		},
		{
			name:     "cnot equation block",
			input:    "Mathematically:\n$$\\text{CNOT}|+\\rangle|-\\rangle = |-\\rangle|-\\rangle$$",
			expected: "Mathematically:\n\n    CNOT|+⟩|-⟩ = |-⟩|-⟩\n",
		},
		{
			name:     "bold markdown styling",
			input:    "This is **Phase kickback** in action.",
			expected: "This is \033[1;36mPhase kickback\033[0m in action.",
		},
		{
			name:     "verbose citation replacement",
			input:    "Fact [[quantum-computing-complete-course-notes]] from notes.",
			expected: "Fact \033[1;36m[1]\033[0m from notes.\n\n\033[1;33mSources:\033[0m\n  \033[1;36m[1]\033[0m quantum-computing-complete-course-notes",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := formatAnswerForTerminal(tc.input)
			if actual != tc.expected {
				t.Errorf("formatAnswerForTerminal(%q) = %q; expected %q", tc.input, actual, tc.expected)
			}
		})
	}
}
