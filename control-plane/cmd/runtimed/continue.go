package main

import (
	"regexp"
	"strings"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

// maxContinueRounds bounds how many times a task re-enters the agent's
// session after a mid-task stop. Three covers every stop seen so far (one
// per section of a page); a model that keeps announcing after that is
// reported as it is rather than driven forever.
const maxContinueRounds = 3

// continuePrompt is what the agent is told on each re-entry. It names the
// failure plainly, because "continue" alone tends to produce a summary of
// what was done rather than the rest of the work.
const continuePrompt = "Your previous message ended by announcing a step you had not done yet. " +
	"That step and everything after it are still to do: do them now, in this session, " +
	"then finish with a message that describes what IS done and anything you could not do. " +
	"Never end on a sentence about what you are about to do."

// nextStepLine matches a final line that announces work instead of reporting
// it: "Now the Offer cards:", "Let me wire the hero.", "Next, the manifest",
// "I'll update the categories".
var nextStepLine = regexp.MustCompile(`(?i)^(now|next|then|let me|let's|i'll|i will|i'm going to|i am going to|going to|time to)\b`)

// announcesNextStep reports whether an agent's final message reads as a
// stop rather than an end: its last non-empty line either ends with a colon
// (a heading for output that never came) or opens with a next-step phrase.
// Short lines only; a long closing paragraph that happens to start with
// "Now" is a report, not an announcement.
func announcesNextStep(final string) bool {
	lines := strings.Split(strings.TrimSpace(final), "\n")
	last := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			last = l
			break
		}
	}
	if last == "" || len(last) > 200 {
		return false
	}
	last = strings.Trim(last, "*_` ")
	if strings.HasSuffix(last, ":") {
		return true
	}
	return nextStepLine.MatchString(last)
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func addUsage(a, b runtime.TokenUsage) runtime.TokenUsage {
	return runtime.TokenUsage{
		Input:      a.Input + b.Input,
		Output:     a.Output + b.Output,
		Reasoning:  a.Reasoning + b.Reasoning,
		CacheRead:  a.CacheRead + b.CacheRead,
		CacheWrite: a.CacheWrite + b.CacheWrite,
		Total:      a.Total + b.Total,
		Cost:       a.Cost + b.Cost,
	}
}
