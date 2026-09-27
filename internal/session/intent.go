package session

import "strings"

// IntentDepth is how far back a checkpoint's reason is looked for. Resume
// reads only a handful of checkpoints for everything else, but a task's reason
// is stated once at its start, and a long task outlives that window by its
// fifth save — after which resume would say what was being done and never why.
const IntentDepth = 50

// IntentFor is why c's task matters, and the earlier checkpoint the reason was
// stated in when c did not state it itself (nil when it did). earlier is the
// project's history before c, newest first.
//
// Only the same task inherits: another task's reason is not this one's. An
// auto record is the exception — its task is the user's first prompt, never
// the agent's wording, so it could never match; it carries on whatever the
// agent's checkpoint before it was doing, and takes that checkpoint's reason.
func IntentFor(c Checkpoint, earlier []Checkpoint) (string, *Checkpoint) {
	if strings.TrimSpace(c.Intent) != "" {
		return c.Intent, nil
	}
	if key := taskKey(c.Task); key != "" {
		for i := range earlier {
			if taskKey(earlier[i].Task) == key && strings.TrimSpace(earlier[i].Intent) != "" {
				return earlier[i].Intent, &earlier[i]
			}
		}
	}
	if c.Auto {
		for i := range earlier {
			if !earlier[i].Auto {
				why, from := IntentFor(earlier[i], earlier[i+1:])
				if from == nil && why != "" {
					from = &earlier[i]
				}
				return why, from
			}
		}
	}
	return "", nil
}

// IntentDropped says c will reach the next agent without a reason although the
// work just before it had one. Inheritance matches the task's wording exactly,
// so a reworded task — "cut the BOM to $118" becoming "keep cutting the BOM
// toward $118" — silently loses a reason the agent believes it already gave.
// Fuzzy matching was the other way out and was ruled out: on tasks this short,
// word overlap cannot tell a rewording from a different task, and handing one
// task another's reason is worse than handing it none.
func IntentDropped(c Checkpoint, earlier []Checkpoint) bool {
	if c.Auto {
		return false
	}
	if why, _ := IntentFor(c, earlier); why != "" {
		return false
	}
	for i := range earlier {
		if !earlier[i].Auto {
			why, _ := IntentFor(earlier[i], earlier[i+1:])
			return why != ""
		}
	}
	return false
}

func taskKey(task string) string {
	return strings.ToLower(strings.Join(strings.Fields(task), " "))
}
