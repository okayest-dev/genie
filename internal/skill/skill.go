// Package skill parses SKILL.md files into structured skill data.
// It extracts YAML frontmatter (name, description, and optional fields)
// and the markdown body that follows.
package skill

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"

	"gopkg.in/yaml.v3"
)

var fmDelimiter = []byte("---")

// ParsedSkill holds the structured data extracted from a SKILL.md file.
type ParsedSkill struct {
	Name                   string
	Description            string
	DisableModelInvocation bool
	ArgumentHint           string
	Body                   string
}

// knownFrontmatterFields lists the recognised frontmatter keys.
var knownFrontmatterFields = map[string]bool{
	"name":                       true,
	"description":                true,
	"disable-model-invocation":   true,
	"argument-hint":              true,
}

// frontmatter is the YAML structure expected between --- delimiters.
// Unknown fields are warned and skipped.
type frontmatter struct {
	Name                   string `yaml:"name"`
	Description            string `yaml:"description"`
	DisableModelInvocation bool   `yaml:"disable-model-invocation"`
	ArgumentHint           string `yaml:"argument-hint"`
}

// Parse takes the raw content of a SKILL.md file and returns a ParsedSkill.
// It expects YAML frontmatter between --- delimiters followed by an optional
// markdown body. Returns an error if frontmatter is missing or required
// fields (name, description) are absent.
func Parse(raw string) (ParsedSkill, error) {
	body, fmBytes, err := splitFrontmatter(raw)
	if err != nil {
		return ParsedSkill{}, err
	}

	var fm frontmatter
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		return ParsedSkill{}, fmt.Errorf("skill: frontmatter: %w", err)
	}

	var rawMap map[string]interface{}
	if err := yaml.Unmarshal(fmBytes, &rawMap); err == nil {
		for key := range rawMap {
			if !knownFrontmatterFields[key] {
				slog.Warn("skill: frontmatter: unknown field skipped", "field", key)
			}
		}
	}

	if fm.Name == "" {
		return ParsedSkill{}, fmt.Errorf("skill: frontmatter: missing required field 'name'")
	}
	if fm.Description == "" {
		return ParsedSkill{}, fmt.Errorf("skill: frontmatter: missing required field 'description'")
	}

	body = strings.TrimRight(body, " \t\n\r")

	slog.Debug("skill parsed", "name", fm.Name, "body_bytes", len(body))

	return ParsedSkill{
		Name:                   fm.Name,
		Description:            fm.Description,
		DisableModelInvocation: fm.DisableModelInvocation,
		ArgumentHint:           fm.ArgumentHint,
		Body:                   body,
	}, nil
}

// splitFrontmatter splits raw into (body, frontmatterYAML, error).
// Frontmatter must start with --- on the first line and have a closing ---
// on a subsequent line. Everything after the closing delimiter is the body.
func splitFrontmatter(raw string) (string, []byte, error) {
	scanner := bytes.NewBufferString(raw)

	firstLine, err := scanner.ReadBytes('\n')
	if err != nil {
		return "", nil, fmt.Errorf("skill: frontmatter: missing opening --- delimiter")
	}
	firstLine = bytes.TrimSpace(firstLine)
	if !bytes.Equal(firstLine, fmDelimiter) {
		return "", nil, fmt.Errorf("skill: frontmatter: missing opening --- delimiter")
	}

	var fmBuf bytes.Buffer
	for {
		line, err := scanner.ReadBytes('\n')
		trimmed := bytes.TrimSpace(line)

		if bytes.Equal(trimmed, fmDelimiter) {
			body := scanner.String()
			return body, fmBuf.Bytes(), nil
		}

		if err != nil {
			return "", nil, fmt.Errorf("skill: frontmatter: missing closing --- delimiter")
		}

		fmBuf.Write(line)
	}
}
