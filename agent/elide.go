package agent

import (
	"fmt"
	"strings"

	"github.com/shakfu/gilda/llm"
)

// Context thresholds, in percent of the window.
const (
	// elideAt is where old tool results are replaced by stubs.
	elideAt = 70
	// warnAt is where the model is told the window is nearly full.
	warnAt = 85
	// fullAt is where a request is refused.
	fullAt = 95
)

// keepResults is the default number of recent tool messages whose results are never elided.
const keepResults = 4

// elideMin is the size in bytes at or under which a result is kept: its stub would save little.
const elideMin = 1024

// bytesPerToken converts elided bytes to an estimate of the tokens they freed, and
// imageTokens does so for an image. The next response's usage replaces the estimate.
const (
	bytesPerToken = 4
	imageTokens   = 1500
)

// contextNote is appended to the last unsent message once the window passes warnAt.
const contextNote = "[gilda: this conversation has used %d%% of the context window. Finish the current task soon; " +
	"prefer small reads with offset and limit, and commands with short output.]"

// elide replaces the content of tool results older than the last KeepResults tool messages
// with a stub naming the call. It changes nothing unless the estimate frees at least a tenth of
// the window, so a session near the threshold does not break the prompt cache on every turn.
func (a *Agent) elide() (results, bytes int, tokens int64) {
	if a.KeepResults < 0 {
		return 0, 0, 0
	}
	keep := a.KeepResults
	if keep == 0 {
		keep = keepResults
	}
	var tools []int // indexes of tool messages, oldest first
	for i, m := range a.History {
		if m.Role == llm.Tool {
			tools = append(tools, i)
		}
	}
	if len(tools) <= keep {
		return 0, 0, 0
	}
	type target struct{ msg, res int }
	var picked []target
	for _, i := range tools[:len(tools)-keep] {
		for j, r := range a.History[i].Results {
			if len(r.Images) > 0 || len(r.Content) > elideMin && !isStub(r.Content) {
				picked = append(picked, target{i, j})
				bytes += len(r.Content)
				tokens += int64(len(r.Content)/bytesPerToken) + int64(len(r.Images))*imageTokens
				for _, img := range r.Images {
					bytes += len(img.Data)
				}
			}
		}
	}
	if tokens < a.Context/10 {
		return 0, 0, 0
	}
	for _, p := range picked {
		r := &a.History[p.msg].Results[p.res]
		size := len(r.Content)
		for _, img := range r.Images {
			size += len(img.Data)
		}
		r.Content, r.Images = stub(callName(a.History, p.msg, r.CallID), size), nil
	}
	return len(picked), bytes, tokens
}

const stubPrefix = "[gilda: elided "

func stub(name string, size int) string {
	return fmt.Sprintf("%s%d bytes of %s output to free context; call it again if you need it]", stubPrefix, size, name)
}

func isStub(s string) bool { return strings.HasPrefix(s, stubPrefix) }

// callName finds the tool a result answers in the assistant message before history[i].
func callName(history []llm.Message, i int, id string) string {
	if i > 0 {
		for _, c := range history[i-1].Calls {
			if c.ID == id {
				return c.Name
			}
		}
	}
	return "tool"
}

// note appends text to the last message of the history, which has not been sent yet, so the
// cached prefix holds.
func (a *Agent) note(text string) {
	n := len(a.History)
	if n == 0 {
		return
	}
	m := &a.History[n-1]
	switch {
	case m.Role == llm.Tool && len(m.Results) > 0:
		r := &m.Results[len(m.Results)-1]
		r.Content += "\n\n" + text
	case m.Role == llm.User:
		m.Text += "\n\n" + text
	}
}

// manageContext elides old results past elideAt, then warns the model once past warnAt. It
// reports whether the window is still past fullAt.
func (a *Agent) manageContext(emit func(Event)) (full bool) {
	if a.Context <= 0 {
		return false
	}
	if a.Used >= a.Context*elideAt/100 {
		if n, b, tok := a.elide(); n > 0 {
			a.Used = max(a.Used-tok, 0)
			emit(Elided{Results: n, Bytes: b})
		}
	}
	pct := a.Used * 100 / a.Context
	switch {
	case pct < warnAt:
		a.warned = false
	case !a.warned:
		a.warned = true
		a.note(fmt.Sprintf(contextNote, pct))
	}
	return pct >= fullAt
}
