package contextmgr

import (
	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/session"
)

// Layer names a context bucket a message is assigned to by role. Layers retain,
// evict, or condense independently.
type Layer string

const (
	// LayerInstruction holds system / agent-instruction messages. Never evicted.
	LayerInstruction Layer = "instruction"

	// LayerDurableIntent holds user and assistant messages. Windowed, compactable.
	LayerDurableIntent Layer = "durable-intent"

	// LayerToolOutput holds tool-result messages (with their ToolCallID).
	// Condensable, net-drop opt-in.
	LayerToolOutput Layer = "tool-output"
)

// markerRole is the reserved JSONL role marking a durable-intent compaction
// marker line — a persisted summary, not a message and part of no layer. See
// ADR-0001.
const markerRole = "compaction"

// Entry is one message indexed in the map, carrying its stable line index and,
// for tool-output entries, its tool-call id.
type Entry struct {
	Line       int
	Msg        llm.Message
	ToolCallID string
}

// Compaction is a durable-intent compaction marker read from the transcript
// during the index build: the persisted summary of the oldest evicted turns.
type Compaction struct {
	Line    int
	Summary string
}

// ContextMap is a derived, layered index over a session's JSONL. It is keyed
// by line index (stable across resume) with a secondary lookup by tool-call-id
// so a specific tool result can be found, pruned, or condensed. Every message
// maps to exactly one layer by role. The map is read-only: it never modifies
// the transcript, which remains the append-only source of truth.
type ContextMap struct {
	byLine      map[int]Entry
	byTool      map[string]int
	layers      map[Layer][]int
	compactions []Compaction
}

// NewMap returns an empty ContextMap, ready to be rebuilt from a session.
func NewMap() *ContextMap {
	return &ContextMap{
		byLine: make(map[int]Entry),
		byTool: make(map[string]int),
		layers: make(map[Layer][]int),
	}
}

// LayerOf returns the layer a message is assigned to by role. Every message
// role maps to exactly one layer; unrecognised roles map to "" (no layer).
func LayerOf(role string) Layer {
	switch role {
	case llm.RoleSystem:
		return LayerInstruction
	case llm.RoleUser, llm.RoleAssistant:
		return LayerDurableIntent
	case llm.RoleTool:
		return LayerToolOutput
	default:
		return ""
	}
}

// RebuildFromSession lazily rebuilds the map by walking the session's
// transcript once: it assigns each message a line index and layer, records
// tool-output entries by tool-call-id, and reads any persisted compaction
// markers. It computes no token counts — those are deferred until the first
// request — and never modifies the transcript.
func (m *ContextMap) RebuildFromSession(s *session.Session) error {
	lines, err := s.Lines()
	if err != nil {
		return err
	}
	m.rebuild(lines)
	return nil
}

// rebuild indexes already-decoded transcript lines, replacing any prior state.
func (m *ContextMap) rebuild(lines []session.TranscriptLine) {
	m.byLine = make(map[int]Entry, len(lines))
	m.byTool = make(map[string]int)
	m.layers = make(map[Layer][]int)
	m.compactions = nil

	for idx, ln := range lines {
		if ln.Role == markerRole {
			m.compactions = append(m.compactions, Compaction{Line: idx, Summary: ln.Content})
			continue
		}
		layer := LayerOf(ln.Role)
		if layer == "" {
			continue
		}
		entry := Entry{Line: idx, Msg: ln.Message(), ToolCallID: ln.ToolCallID}
		m.byLine[idx] = entry
		m.layers[layer] = append(m.layers[layer], idx)
		// A tool result is addressable by its tool-call id so it can be found,
		// pruned, or condensed. Tool results that carry a ToolCallID are indexed
		// here by that id regardless of the layer role assigns them (layers are
		// still assigned strictly by role).
		if ln.ToolCallID != "" {
			m.byTool[ln.ToolCallID] = idx
		}
	}
}

// Message returns the entry at the given session-line index.
func (m *ContextMap) Message(line int) (Entry, bool) {
	e, ok := m.byLine[line]
	return e, ok
}

// ByToolCallID returns the tool-output entry for a tool result, found by its
// tool-call id.
func (m *ContextMap) ByToolCallID(id string) (Entry, bool) {
	line, ok := m.byTool[id]
	if !ok {
		return Entry{}, false
	}
	return m.byLine[line], true
}

// Layer returns the ordered entries of a layer, in transcript order.
func (m *ContextMap) Layer(l Layer) []Entry {
	var out []Entry
	for _, idx := range m.layers[l] {
		out = append(out, m.byLine[idx])
	}
	return out
}

// Compactions returns the compaction markers read during the index build, in
// transcript order.
func (m *ContextMap) Compactions() []Compaction {
	return m.compactions
}

// Len returns the number of indexed messages.
func (m *ContextMap) Len() int {
	return len(m.byLine)
}
