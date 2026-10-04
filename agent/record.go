package agent

import "encoding/json"

// Record maps an event to a JSON-ready record with a "type" field: text, reasoning,
// tool_start, tool_call, tool_result, retry, elided, task or turn. An error becomes its message, since
// error values marshal as {}. The CLI's --json output prints all but text, reasoning and
// tool_start.
func Record(e Event) map[string]any {
	switch e := e.(type) {
	case Text:
		return map[string]any{"type": "text", "text": e.Text}
	case Reasoning:
		return map[string]any{"type": "reasoning", "text": e.Text}
	case ToolStart:
		return map[string]any{"type": "tool_start", "name": e.Name}
	case ToolCall:
		return map[string]any{"type": "tool_call", "id": e.Call.ID, "name": e.Call.Name,
			"label": e.Label, "arguments": rawJSON(e.Call.Arguments)}
	case ToolResult:
		rec := map[string]any{"type": "tool_result", "id": e.Call.ID, "name": e.Call.Name, "label": e.Label,
			"ok": e.Err == nil && !e.Result.Failed, "summary": e.Result.Summary, "output": e.Result.Output}
		if e.Err != nil {
			rec["error"] = e.Err.Error()
		}
		return rec
	case Response:
		return map[string]any{"type": "turn", "text": e.Text, "stop": e.Stop, "usage": e.Usage}
	case Retry:
		return map[string]any{"type": "retry", "attempt": e.Attempt, "reason": e.Reason}
	case Elided:
		return map[string]any{"type": "elided", "results": e.Results, "bytes": e.Bytes}
	case TaskDone:
		return map[string]any{"type": "task", "turns": e.Turns, "usage": e.Usage}
	}
	return nil
}

// rawJSON embeds valid JSON arguments as they are and quotes anything else, such as arguments
// a cut-off response left unfinished.
func rawJSON(s string) json.RawMessage {
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	data, _ := json.Marshal(s)
	return data
}
