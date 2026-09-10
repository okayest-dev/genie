package skill

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    ParsedSkill
		wantErr string
	}{
		{
			name: "valid full",
			input: `---
name: my-skill
description: Does something useful.
disable-model-invocation: true
argument-hint: <file>
---
Body content goes here.
`,
			want: ParsedSkill{
				Name:                     "my-skill",
				Description:              "Does something useful.",
				DisableModelInvocation:   true,
				ArgumentHint:             "<file>",
				Body:                     "Body content goes here.",
			},
		},
		{
			name: "valid minimal",
			input: `---
name: minimal
description: A minimal skill.
---
`,
			want: ParsedSkill{
				Name:        "minimal",
				Description: "A minimal skill.",
			},
		},
		{
			name: "missing name",
			input: `---
description: Missing the name field.
---
Body`,
			wantErr: "name",
		},
		{
			name: "missing description",
			input: `---
name: no-desc
---
Body`,
			wantErr: "description",
		},
		{
			name:    "no frontmatter",
			input:   "Just plain text with no frontmatter delimiters.",
			wantErr: "frontmatter",
		},
		{
			name: "unknown fields warned",
			input: `---
name: future-proof
description: Has an unknown field.
some-future-field: value
---
Body`,
			want: ParsedSkill{
				Name:        "future-proof",
				Description: "Has an unknown field.",
				Body:        "Body",
			},
		},
		{
			name: "empty body",
			input: `---
name: empty-body
description: No body content.
---`,
			want: ParsedSkill{
				Name:        "empty-body",
				Description: "No body content.",
			},
		},
		{
			name: "body trailing whitespace",
			input: `---
name: trailing
description: Body with trailing whitespace.
---
Some body content.

`,
			want: ParsedSkill{
				Name:        "trailing",
				Description: "Body with trailing whitespace.",
				Body:        "Some body content.",
			},
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: "frontmatter",
		},
		{
			name:    "only delimiters",
			input:   "---\n---",
			wantErr: "name",
		},
		{
			name:    "unclosed frontmatter",
			input:   "---\nname: unclosed\ndescription: Never closed.",
			wantErr: "frontmatter",
		},
		{
			name: "explicit false boolean",
			input: `---
name: explicit-false
description: Explicitly false.
disable-model-invocation: false
---`,
			want: ParsedSkill{
				Name:        "explicit-false",
				Description: "Explicitly false.",
			},
		},
		{
			name: "only name missing description",
			input: `---
name: only-name
---`,
			wantErr: "description",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Parse() error = nil, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got.Name != tt.want.Name {
				t.Errorf("Name = %q, want %q", got.Name, tt.want.Name)
			}
			if got.Description != tt.want.Description {
				t.Errorf("Description = %q, want %q", got.Description, tt.want.Description)
			}
			if got.DisableModelInvocation != tt.want.DisableModelInvocation {
				t.Errorf("DisableModelInvocation = %v, want %v", got.DisableModelInvocation, tt.want.DisableModelInvocation)
			}
			if got.ArgumentHint != tt.want.ArgumentHint {
				t.Errorf("ArgumentHint = %q, want %q", got.ArgumentHint, tt.want.ArgumentHint)
			}
			if got.Body != tt.want.Body {
				t.Errorf("Body = %q, want %q", got.Body, tt.want.Body)
			}
		})
	}
}

func TestParseUnknownFieldsWarns(t *testing.T) {
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() {
		slog.SetDefault(slog.New(slog.NewTextHandler(nil, &slog.HandlerOptions{Level: slog.LevelWarn})))
	})

	input := `---
name: warn-test
description: Should warn about unknown fields.
mystery-field: hello
---
`
	if _, err := Parse(input); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !strings.Contains(buf.String(), "unknown field skipped") {
		t.Errorf("expected warning about unknown field, got log: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "mystery-field") {
		t.Errorf("expected warning to mention the field name, got log: %s", buf.String())
	}
}
