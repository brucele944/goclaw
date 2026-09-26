package providers

import (
	"encoding/json"
	"io"
	"testing"
)

// TestBuildStreamJSONInput_MimeRouting verifies that buildStreamJSONInput
// picks the correct Anthropic content block type based on MIME:
//   - application/pdf → "document"
//   - image/*         → "image"
//
// Regression guard: earlier versions hardcoded "image" for every block,
// causing PDF passthrough to fail because the Anthropic API rejects
// image blocks with non-image MIME types.
func TestBuildStreamJSONInput_MimeRouting(t *testing.T) {
	cases := []struct {
		name      string
		images    []ImageContent
		wantTypes []string
	}{
		{
			name: "png image → image block",
			images: []ImageContent{
				{MimeType: "image/png", Data: "abc"},
			},
			wantTypes: []string{"image"},
		},
		{
			name: "pdf → document block",
			images: []ImageContent{
				{MimeType: "application/pdf", Data: "abc"},
			},
			wantTypes: []string{"document"},
		},
		{
			name: "mixed png + pdf → image then document",
			images: []ImageContent{
				{MimeType: "image/jpeg", Data: "xxx"},
				{MimeType: "application/pdf", Data: "yyy"},
			},
			wantTypes: []string{"image", "document"},
		},
		{
			name: "unknown MIME falls back to image",
			images: []ImageContent{
				{MimeType: "application/octet-stream", Data: "zzz"},
			},
			wantTypes: []string{"image"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := buildStreamJSONInput("describe", tc.images)
			raw, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("read stdin: %v", err)
			}

			var msg struct {
				Message struct {
					Content []map[string]any `json:"content"`
				} `json:"message"`
			}
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Fatalf("parse stream-json: %v\nraw: %s", err, raw)
			}

			// Expect N media blocks + 1 trailing text block.
			wantLen := len(tc.wantTypes) + 1
			if got := len(msg.Message.Content); got != wantLen {
				t.Fatalf("content blocks = %d, want %d\nraw: %s", got, wantLen, raw)
			}

			for i, wantType := range tc.wantTypes {
				gotType, _ := msg.Message.Content[i]["type"].(string)
				if gotType != wantType {
					t.Errorf("block[%d].type = %q, want %q", i, gotType, wantType)
				}
				source, _ := msg.Message.Content[i]["source"].(map[string]any)
				if source == nil {
					t.Errorf("block[%d].source is nil", i)
					continue
				}
				if gotMime, _ := source["media_type"].(string); gotMime != tc.images[i].MimeType {
					t.Errorf("block[%d].source.media_type = %q, want %q", i, gotMime, tc.images[i].MimeType)
				}
			}

			// Trailing text block.
			last := msg.Message.Content[wantLen-1]
			if last["type"] != "text" {
				t.Errorf("trailing block type = %v, want text", last["type"])
			}
			if last["text"] != "describe" {
				t.Errorf("trailing block text = %v, want describe", last["text"])
			}
		})
	}
}

// TestBuildStreamJSONInput_NoText covers the edge case where the caller
// passes images with an empty prompt — no text block should be emitted.
func TestBuildStreamJSONInput_NoText(t *testing.T) {
	r := buildStreamJSONInput("", []ImageContent{
		{MimeType: "image/png", Data: "abc"},
	})
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	var msg struct {
		Message struct {
			Content []map[string]any `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("parse stream-json: %v", err)
	}
	if len(msg.Message.Content) != 1 {
		t.Errorf("content blocks = %d, want 1 (image only)", len(msg.Message.Content))
	}
}

// TestDisallowedCLITools_NoStaleToolNames guards against deny rules that name
// tools the current Claude CLI no longer registers. A stale name makes the CLI
// print `Permission deny rule "<name>" matches no known tool` on every
// invocation (including background episodic summarization), which buries real
// errors. TodoRead/NotebookRead were removed from the CLI in 2.x; their
// write-side counterparts must stay blocked.
func TestDisallowedCLITools_NoStaleToolNames(t *testing.T) {
	blocked := disallowedCLITools(nil) // nil = fail closed: everything blocked

	got := make(map[string]bool, len(blocked))
	for _, name := range blocked {
		got[name] = true
	}

	for _, stale := range []string{"TodoRead", "NotebookRead"} {
		if got[stale] {
			t.Errorf("disallowedCLITools includes %q, which current Claude CLI no longer registers (causes 'matches no known tool' warnings)", stale)
		}
	}
	for _, required := range []string{"TodoWrite", "NotebookEdit", "Glob", "Grep"} {
		if !got[required] {
			t.Errorf("disallowedCLITools missing %q — native tool without GoClaw equivalent must stay blocked", required)
		}
	}
}

func TestClaudeCLIRemembersUnknownDisallowedToolRules(t *testing.T) {
	p := NewClaudeCLIProvider("claude")

	if _, retry := p.noteInvalidDisallowedTool(`Permission deny rule "NotebookRead" matches no known tool — check for typos.`); !retry {
		t.Fatalf("first unknown tool observation should request retry")
	}
	if _, retry := p.noteInvalidDisallowedTool(`Permission deny rule "NotebookRead" matches no known tool — check for typos.`); retry {
		t.Fatalf("repeated unknown tool observation should not request another retry")
	}

	filtered := p.filterInvalidDisallowedTools([]string{"Bash", "NotebookRead", "TodoWrite"})
	want := []string{"Bash", "TodoWrite"}
	if len(filtered) != len(want) {
		t.Fatalf("filtered tools = %v, want %v", filtered, want)
	}
	for i := range want {
		if filtered[i] != want[i] {
			t.Fatalf("filtered tools = %v, want %v", filtered, want)
		}
	}
}

func TestClaudeCLIDisallowedToolsExcludeRemovedReadOnlyTools(t *testing.T) {
	blocked := disallowedCLITools(nil)
	for _, removed := range []string{"NotebookRead", "TodoRead"} {
		for _, got := range blocked {
			if got == removed {
				t.Fatalf("disallowedCLITools included removed Claude CLI tool %q in %v", removed, blocked)
			}
		}
	}

	for _, want := range []string{"NotebookEdit", "TodoWrite"} {
		found := false
		for _, got := range blocked {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("disallowedCLITools missing still-supported tool %q in %v", want, blocked)
		}
	}
}

// TestEncodeClaudeProjectDir pins the Claude CLI project-directory encoding.
// The expected values were read back from ~/.claude/projects/ after running
// Claude Code 2.1.282 in each probe directory; a narrower replacement set
// (separators, "_", ".", ":" only) misses spaces and punctuation, which makes
// sessionFileExists point at a directory the CLI never writes and turns every
// follow-up turn into "Session ID <uuid> is already in use."
func TestEncodeClaudeProjectDir(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		want string
	}{
		{
			name: "windows path with space in user name",
			dir:  `C:\Users\Bruce Le\.goclaw\data\cli-workspaces\default`,
			want: "C--Users-Bruce-Le--goclaw-data-cli-workspaces-default",
		},
		{
			name: "windows path with space underscore dot colon at",
			dir:  `C:\AppData\Local\Temp\enc-probe\a b_c.d@e#f+g(h),i'j`,
			want: "C--AppData-Local-Temp-enc-probe-a-b-c-d-e-f-g-h--i-j",
		},
		{
			// Non-ASCII BMP rune (U+1EC3) = one UTF-16 code unit = one dash.
			name: "non-ascii bmp rune collapses to one dash",
			dir:  `C:\AppData\Local\Temp\enc-probe2\a+@bểc`,
			want: "C--AppData-Local-Temp-enc-probe2-a--b-c",
		},
		{
			// Non-BMP rune (U+1F600) = surrogate pair = two code units = two dashes.
			name: "non-bmp rune collapses to two dashes",
			dir:  `C:\AppData\Local\Temp\enc-probe3\x😀y`,
			want: "C--AppData-Local-Temp-enc-probe3-x--y",
		},
		{
			name: "unix path",
			dir:  "/home/jane doe/.goclaw/data",
			want: "-home-jane-doe--goclaw-data",
		},
		{
			name: "already hyphenated segments are preserved",
			dir:  `C:\goclaw\data\cli-workspaces\agent-little-fox-ws-direct-adbce685`,
			want: "C--goclaw-data-cli-workspaces-agent-little-fox-ws-direct-adbce685",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := encodeClaudeProjectDir(tc.dir); got != tc.want {
				t.Errorf("encodeClaudeProjectDir(%q) = %q, want %q", tc.dir, got, tc.want)
			}
		})
	}
}
