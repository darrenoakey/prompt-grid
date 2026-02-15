package discord

import (
	"reflect"
	"testing"
)

func TestDiscordContentToInputLines(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "single line adds enter",
			content: "/commit",
			want:    []string{"/commit"},
		},
		{
			name:    "embedded unix newlines map to enter",
			content: "line1\nline2",
			want:    []string{"line1", "line2"},
		},
		{
			name:    "windows newlines normalized",
			content: "line1\r\nline2",
			want:    []string{"line1", "line2"},
		},
		{
			name:    "trailing newline does not double append",
			content: "line1\n",
			want:    []string{"line1"},
		},
		{
			name:    "empty line in middle preserved",
			content: "line1\n\nline3",
			want:    []string{"line1", "", "line3"},
		},
		{
			name:    "empty content returns nil",
			content: "",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := discordContentToInputLines(tt.content)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("discordContentToInputLines(%q) = %#v, want %#v", tt.content, got, tt.want)
			}
		})
	}
}

func TestLineToKeyArgs(t *testing.T) {
	tests := []struct {
		name string
		line string
		want []string
	}{
		{
			name: "simple chars",
			line: "ab1",
			want: []string{"a", "b", "1"},
		},
		{
			name: "space and tab",
			line: "a \tb",
			want: []string{"a", "Space", "Tab", "b"},
		},
		{
			name: "empty",
			line: "",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lineToKeyArgs(tt.line)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("lineToKeyArgs(%q) = %#v, want %#v", tt.line, got, tt.want)
			}
		})
	}
}
