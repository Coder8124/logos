package eval

import (
	"fmt"
	"time"
)

// The suite.
//
// Every case is hand-written, and every case is trying to break something
// specific. Generated scenarios were considered and rejected: a model asked for
// three hundred handoffs produces three hundred variations on the same easy
// one, and gold labels from a model are labels you cannot audit.
//
// The world is shared across cases on purpose — a smart-glasses company with a
// bill of materials that will not close, a factory missing yield, and a
// schedule with a critical path. Reusing one setting means a system cannot do
// well by pattern-matching a case's vocabulary, and it lets the harder cases
// depend on facts established elsewhere in the same fiction.
//
// Roughly a third of the suite is marked KnownWeakness. Those are not
// aspirational; they are cases brain fails today, several found by an agent
// picking apart brain's own handoff output during a live test. A benchmark
// whose author chooses the categories is a benchmark its author wins, and the
// only defence is to write the losses down first.

// The clock is anchored to now, and the offsets are what is fixed.
//
// It was a constant at first, which was wrong in a way that hid a whole family
// of failures: pinned to 2026-01-01 and run in August, a note written "thirteen
// days ago" is really eight months old, and any system reporting the age of its
// own context reports the true one. The staleness cases were unwinnable and the
// suite was measuring the wrong thing. What has to stay stable between runs is
// the *interval* — thirteen days before the question — not the wall date.
var benchNow = time.Now().Unix()

const day = 86400

func ago(days int) int64 { return benchNow - int64(days)*day }

// ---------------------------------------------------------------------------
// Event constructors, kept terse so the scenarios below read as prose.
// ---------------------------------------------------------------------------

func doc(days int, project, title, text string) Event {
	return Event{TS: ago(days), Actor: "user", Kind: KindDoc, Project: project, Title: title, Text: text}
}

func note(days int, actor, project, text string) Event {
	return Event{TS: ago(days), Actor: actor, Kind: KindNote, Project: project, Text: text}
}

func said(days int, text string) Event {
	return Event{TS: ago(days), Actor: "user", Kind: KindFact, Text: text}
}

func msg(days int, actor, text string) Event {
	return Event{TS: ago(days), Actor: actor, Kind: KindMessage, Text: text}
}

// Suite returns the whole benchmark.
func Suite() []Scenario {
	var s []Scenario
	s = append(s, continuity()...)
	s = append(s, memoryCases()...)
	s = append(s, durability()...)
	return s
}

// ---------------------------------------------------------------------------
// Continuity — what survives the boundary between two agents.
// ---------------------------------------------------------------------------

func continuity() []Scenario {
	return []Scenario{
		{
			ID: "handoff-failed-approaches", Family: "continuity", Skill: "negative-knowledge",
			Why:   "The three things already ruled out are the expensive knowledge and the first thing lost.",
			Known: KnownStrength,
			Setup: []Event{
				doc(30, "kestrel-one", "Kestrel One", "Smart glasses, $249 retail. Target BOM $118, actual $141.20."),
				note(6, "claude", "kestrel-one", "Re-quoted the waveguide with Lumus — no movement under 10k units."),
				note(6, "claude", "kestrel-one", "Tried dropping the second microphone; audio team vetoed, beamforming needs two."),
				{
					TS: ago(5), Actor: "claude", Kind: KindCheckpoint, Project: "kestrel-one",
					Task:      "cut the BOM from $141.20 to the $118 target",
					Text:      "Down to $137.40. The remaining gap is concentrated in the optics stack.",
					Decisions: []string{"Keep the dual-mic array; audio quality is a reviewable feature"},
					Failed: []string{
						"Re-quoting the waveguide with Lumus — no movement under 10k units",
						"Dropping the second microphone — vetoed by audio, beamforming needs two",
						"Switching to a plastic frame — fails the drop test at 1.2m",
					},
					Questions: []string{"Can we move the display driver to the cheaper Himax part without a recert?"},
					Next:      "Quote the single-source display driver alternatives",
				},
			},
			Query: Query{Task: "keep cutting the BOM toward target", Project: "kestrel-one", Agent: "cursor", Budget: 4000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{
					{Label: "waveguide re-quote failed", Any: []string{"lumus", "waveguide"}},
					{Label: "mic removal vetoed", Any: []string{"second microphone", "beamforming", "dual-mic"}},
					{Label: "plastic frame failed drop test", Any: []string{"plastic frame", "drop test", "1.2m"}},
					{Label: "current BOM figure", Any: []string{"137.40", "$137"}},
					{Label: "next step", Any: []string{"display driver"}},
				},
			},
		},
		{
			ID: "handoff-uncommitted-notes", Family: "continuity", Skill: "died-mid-task",
			Why:   "An agent killed before it could check in leaves only working notes; those are the whole record.",
			Known: KnownStrength,
			Setup: []Event{
				doc(30, "ota-firmware", "OTA firmware", "Over-the-air update path for Kestrel One. Blocked: no spare flash budgeted for an A/B partition scheme."),
				note(2, "claude", "ota-firmware", "Certification objection is wrong — a firmware-only OTA does not re-trigger the RF filing, confirmed against the FCC scope."),
				note(2, "claude", "ota-firmware", "RMA math: at 2.1% return rate and $41 handling, one field-fixable bug pays for the whole OTA effort."),
				note(2, "claude", "ota-firmware", "Vault has NO flash size number for the SoC — only that it was costed at 'the smaller part' on the $27.80 SoC+memory line. Cannot size an A/B scheme without the part number."),
			},
			Query: Query{Task: "pick up the OTA firmware work", Project: "ota-firmware", Agent: "cursor", Budget: 4000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{
					{Label: "certification objection resolved", Any: []string{"does not re-trigger", "rf filing", "fcc"}},
					{Label: "RMA math", Any: []string{"2.1%", "$41", "return rate"}},
					{Label: "the open blocker, in full", Any: []string{"part number", "cannot size"}},
					{Label: "the $27.80 line it was costed on", Any: []string{"27.80"}},
				},
				Signal: []Fact{
					{Label: "marked as never checkpointed", Any: []string{"not yet checkpointed", "uncommitted", "working note"}},
				},
			},
		},
		{
			ID: "handoff-attribution", Family: "continuity", Skill: "attribution",
			Why:   "When two agents contributed, which of them found a thing is part of the finding.",
			Known: KnownStrength,
			Setup: []Event{
				doc(30, "yield", "Bonding yield", "Display bonding runs at 71 percent first-pass."),
				note(4, "claude", "yield", "Traced the yield loss to the ACF bonding temperature ramp, not the alignment stage."),
				note(3, "cursor", "yield", "Vendor confirmed the ramp is fixed in firmware and cannot be tuned on our units."),
			},
			Query: Query{Task: "continue the yield investigation", Project: "yield", Agent: "codex", Budget: 4000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{
					{Label: "the ACF ramp finding", Any: []string{"acf", "temperature ramp"}},
					{Label: "the vendor answer", Any: []string{"fixed in firmware", "cannot be tuned"}},
				},
				Signal: []Fact{
					{Label: "both agents named", All: []string{"claude", "cursor"}},
				},
			},
		},
		{
			ID: "handoff-cold-start", Family: "continuity", Skill: "cold-start",
			Why:   "The arriving agent knows only the project name. Everything else has to come from the store.",
			Known: KnownStrength,
			Setup: []Event{
				doc(40, "kestrel-one", "Kestrel One", "Smart glasses, $249 retail, ship date November 12."),
				{
					TS: ago(3), Actor: "claude", Kind: KindCheckpoint, Project: "kestrel-one",
					Task:      "decide whether to hold the November ship date",
					Text:      "Tooling freeze is the binding constraint, not the BOM.",
					Decisions: []string{"Hold the date; slip the second colourway to a spring refresh"},
					Failed:    []string{"Compressing the certification window — the lab has no slot before October"},
					Next:      "Get Tomas to confirm the tooling freeze date in writing",
				},
			},
			Query: Query{Task: "continue", Project: "kestrel-one", Agent: "cursor", Budget: 4000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{
					{Label: "the actual task", Any: []string{"november", "ship date"}},
					{Label: "the decision", Any: []string{"hold the date", "colourway", "spring refresh"}},
					{Label: "the ruled-out approach", Any: []string{"certification window", "no slot before october"}},
					{Label: "the next action", Any: []string{"tomas", "tooling freeze"}},
				},
			},
		},
		{
			ID: "handoff-scope-isolation", Family: "continuity", Skill: "scope",
			Why:   "Two projects in one store. Resuming one must not import the other's ruled-out approaches.",
			Known: KnownStrength,
			Setup: []Event{
				doc(30, "kestrel-one", "Kestrel One", "Smart glasses hardware programme."),
				doc(30, "website", "Website rebuild", "Marketing site rebuild ahead of launch."),
				{
					TS: ago(4), Actor: "claude", Kind: KindCheckpoint, Project: "website",
					Task:   "pick a CMS",
					Failed: []string{"Contentful — pricing tier jumps at exactly our seat count"},
					Next:   "Trial Sanity for a week",
				},
				{
					TS: ago(3), Actor: "claude", Kind: KindCheckpoint, Project: "kestrel-one",
					Task:   "close the BOM gap",
					Failed: []string{"Re-quoting the waveguide — no movement under 10k units"},
					Next:   "Quote the display driver alternatives",
				},
			},
			Query: Query{Task: "keep working the BOM", Project: "kestrel-one", Agent: "cursor", Budget: 4000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{
					{Label: "this project's failed approach", Any: []string{"waveguide"}},
					{Label: "this project's next step", Any: []string{"display driver"}},
				},
				Avoid: []Fact{
					{Label: "the other project's CMS decision", Any: []string{"contentful", "sanity"}},
				},
			},
		},
		{
			ID: "handoff-budget-discipline", Family: "continuity", Skill: "budget",
			Why:   "At a tight ceiling, the ruled-out approaches must outrank background prose.",
			Known: KnownStrength,
			Setup: append(
				noise(20, "kestrel-one", "Routine standup note"),
				doc(30, "kestrel-one", "Kestrel One", "Smart glasses, $249 retail."),
				Event{
					TS: ago(2), Actor: "claude", Kind: KindCheckpoint, Project: "kestrel-one",
					Task:   "close the BOM gap",
					Failed: []string{"Re-quoting the waveguide — no movement under 10k units"},
					Next:   "Quote the display driver alternatives",
				},
			),
			Query: Query{Task: "continue the BOM work", Project: "kestrel-one", Agent: "cursor", Budget: 700, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{
					{Label: "failed approach survives the squeeze", Any: []string{"waveguide"}},
					{Label: "next step survives the squeeze", Any: []string{"display driver"}},
				},
			},
		},
		{
			ID: "handoff-noise-resistance", Family: "continuity", Skill: "distractors",
			Why:   "Forty routine notes around one checkpoint. Volume must not bury the handoff.",
			Known: KnownStrength,
			Setup: append(
				noise(40, "kestrel-one", "Standup: no blockers, continuing as planned"),
				Event{
					TS: ago(1), Actor: "claude", Kind: KindCheckpoint, Project: "kestrel-one",
					Task:   "resolve the thermal throttling on the left temple",
					Failed: []string{"Adding a copper spreader — no room once the battery moved forward"},
					Next:   "Test the 400MHz cap against the review benchmark suite",
				},
			),
			Query: Query{Task: "continue the thermal work", Project: "kestrel-one", Agent: "cursor", Budget: 4000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{
					{Label: "the failed approach", Any: []string{"copper spreader"}},
					{Label: "the next step", Any: []string{"400mhz", "benchmark"}},
				},
			},
		},
		{
			ID: "handoff-decision-rationale", Family: "continuity", Skill: "rationale",
			Why:   "A decision without its reason gets re-litigated by the next agent.",
			Known: KnownStrength,
			Setup: []Event{
				doc(20, "kestrel-one", "Kestrel One", "Smart glasses programme."),
				{
					TS: ago(2), Actor: "claude", Kind: KindCheckpoint, Project: "kestrel-one",
					Task:      "choose the battery chemistry",
					Decisions: []string{"Stay with the pouch cell rather than moving to cylindrical, because the temple geometry cannot take a 14mm diameter without widening the hinge"},
					Next:      "Confirm the pouch supplier's second source",
				},
			},
			Query: Query{Task: "continue the battery decision", Project: "kestrel-one", Agent: "cursor", Budget: 4000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{
					{Label: "the decision", Any: []string{"pouch cell"}},
					{Label: "the reason, not just the decision", Any: []string{"14mm", "hinge", "temple geometry"}},
				},
			},
		},
		{
			ID: "handoff-chain-three-agents", Family: "continuity", Skill: "multi-hop-handoff",
			Why:   "A ruled out something in the first session. C, two handoffs later, must still not retry it.",
			Known: KnownStrength,
			Setup: []Event{
				doc(30, "kestrel-one", "Kestrel One", "Smart glasses programme."),
				{
					TS: ago(9), Actor: "claude", Kind: KindCheckpoint, Project: "kestrel-one",
					Task:   "reduce weight below 48g",
					Failed: []string{"Thinning the magnesium frame — fails the torsion spec at 0.8mm"},
					Next:   "Look at the battery pack carrier",
				},
				{
					TS: ago(5), Actor: "cursor", Kind: KindCheckpoint, Project: "kestrel-one",
					Task:   "reduce weight below 48g",
					Text:   "Carrier redesign saves 2.1g.",
					Failed: []string{"Removing the carrier entirely — the cell needs the constraint under vibration"},
					Next:   "Look at the hinge assembly",
				},
			},
			Query: Query{Task: "continue reducing weight", Project: "kestrel-one", Agent: "codex", Budget: 4000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{
					{Label: "the most recent failure", Any: []string{"removing the carrier", "vibration"}},
					{Label: "the earlier failure two handoffs back", Any: []string{"magnesium", "torsion", "0.8mm"}},
					{Label: "progress so far", Any: []string{"2.1g"}},
				},
			},
		},
		{
			ID: "handoff-open-question", Family: "continuity", Skill: "open-questions",
			Why:   "An unresolved question is work in progress; losing it means somebody answers it twice.",
			Known: KnownStrength,
			Setup: []Event{
				doc(20, "ota-firmware", "OTA firmware", "Over-the-air update path."),
				{
					TS: ago(2), Actor: "claude", Kind: KindCheckpoint, Project: "ota-firmware",
					Task:      "decide whether OTA is viable",
					Questions: []string{"Does the eMMC part have enough spare capacity for a second slot, or do we need a bigger part?"},
					Next:      "Ask Tomas for the eMMC part number",
				},
			},
			Query: Query{Task: "continue the OTA assessment", Project: "ota-firmware", Agent: "cursor", Budget: 4000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{
					{Label: "the open question", Any: []string{"emmc", "spare capacity", "second slot"}},
				},
			},
		},

		// ---- the ones we lose ------------------------------------------------

		{
			ID: "handoff-stale-context", Family: "continuity", Skill: "staleness",
			Why:   "Notes are thirteen days old against a moving schedule. Nothing marks them as old.",
			Known: KnownStrength,
			Setup: []Event{
				doc(60, "kestrel-one", "Kestrel One", "Ship date November 12. Tooling freeze six weeks prior."),
				note(13, "claude", "kestrel-one", "We have about sixteen days before the tooling freeze, so there is room for one more DVT spin."),
			},
			Query: Query{Task: "plan the next DVT spin", Project: "kestrel-one", Agent: "cursor", Budget: 4000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{
					{Label: "the note itself", Any: []string{"dvt", "tooling freeze"}},
				},
				Signal: []Fact{
					{Label: "the age of the estimate is surfaced", Any: []string{"13 days", "thirteen days", "days ago", "as of", "may be out of date", "stale"}},
				},
			},
		},
		{
			ID: "handoff-superseded-plan", Family: "continuity", Skill: "supersession",
			Why:   "The plan in the checkpoint was abandoned by the user afterwards. Replaying it as current is worse than silence.",
			Known: KnownStrength,
			Setup: []Event{
				doc(30, "kestrel-one", "Kestrel One", "Smart glasses programme."),
				{
					TS: ago(6), Actor: "claude", Kind: KindCheckpoint, Project: "kestrel-one",
					Task: "close the BOM gap",
					Next: "Move the display driver to the Himax part to save $4.10",
				},
				msg(2, "user", "We are not moving to the Himax display driver — legal flagged the licensing terms and we are staying with Solomon. Drop that thread entirely."),
			},
			Query: Query{Task: "continue the BOM work", Project: "kestrel-one", Agent: "cursor", Budget: 4000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{
					{Label: "the current position", Any: []string{"solomon", "legal", "licensing"}},
				},
				Avoid: []Fact{
					{Label: "the abandoned plan presented as next", Any: []string{"move the display driver to the himax", "next: move the display driver"}},
				},
			},
		},
		{
			ID: "handoff-conflicting-sources", Family: "continuity", Skill: "conflict",
			Why:   "Prose says one thing, the exported data says another. Surfacing only the prose confirms the wrong answer with a citation attached.",
			Known: KnownWeakness,
			Setup: []Event{
				doc(30, "ota-firmware", "OTA firmware", "Blocked: there is no spare flash budgeted for an A/B partition scheme, so OTA is not viable on this hardware."),
				doc(28, "ota-firmware", "BOM export (rev 14)", "Line 22: SoC+memory module, 64GB eMMC, $27.80. Line 23: no discrete boot flash — boot from eMMC."),
			},
			Query: Query{Task: "is an A/B partition scheme affordable on this hardware?", Project: "ota-firmware", Agent: "cursor", Budget: 4000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{
					{Label: "the contradicting data line", Any: []string{"64gb", "emmc"}},
				},
				Signal: []Fact{
					{Label: "the conflict is flagged rather than resolved silently", Any: []string{"conflict", "contradict", "disagree", "however", "but the"}},
				},
			},
		},
		{
			ID: "handoff-abandoned-thread", Family: "continuity", Skill: "recency-conflict",
			Why:   "The freshest note is not the most important one; the older blocker still blocks.",
			Known: KnownWeakness,
			Setup: []Event{
				doc(30, "kestrel-one", "Kestrel One", "Smart glasses programme."),
				note(8, "claude", "kestrel-one", "HARD BLOCKER: the certification lab has no slot before October 14; nothing ships before that date regardless of tooling."),
				note(7, "claude", "kestrel-one", "Renamed the CAD files to the new convention."),
				note(6, "claude", "kestrel-one", "Updated the colour swatches in the spec sheet."),
				note(5, "claude", "kestrel-one", "Fixed a typo in the packaging copy."),
				note(4, "claude", "kestrel-one", "Archived the old renders."),
			},
			Query: Query{Task: "what is standing between us and shipping?", Project: "kestrel-one", Agent: "cursor", Budget: 700, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{
					{Label: "the blocker, not the housekeeping", Any: []string{"october 14", "certification lab"}},
				},
				Avoid: []Fact{
					{Label: "housekeeping crowding out the blocker", Any: []string{"colour swatches", "typo in the packaging"}},
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Memory — what the store knows about the user and their world.
// ---------------------------------------------------------------------------

func memoryCases() []Scenario {
	return []Scenario{
		{
			ID: "recall-direct", Family: "memory", Skill: "recall",
			Why:   "The floor. A stated fact, asked for directly.",
			Known: KnownStrength,
			Setup: append(noiseFacts(30),
				said(20, "Our contract manufacturer is Pegatron, in the Suzhou plant."),
			),
			Query: Query{Task: "who manufactures our hardware?", Budget: 2000, Now: benchNow},
			Gold:  Gold{Carry: []Fact{{Label: "the manufacturer", Any: []string{"pegatron"}}}},
		},
		{
			ID: "recall-preference", Family: "memory", Skill: "preference",
			Why:   "A standing preference stated once among distractors.",
			Known: KnownStrength,
			Setup: append(noiseFacts(30),
				said(25, "I prefer written proposals over meetings — send me a doc and I will comment on it."),
			),
			Query: Query{Task: "how should I bring a proposal to you?", Budget: 2000, Now: benchNow},
			Gold:  Gold{Carry: []Fact{{Label: "the preference", Any: []string{"written proposal", "send me a doc"}}}},
		},
		{
			ID: "recall-lexical-needle", Family: "memory", Skill: "lexical",
			Why:   "A one-off with a distinctive term the embedding blurs but exact match finds.",
			Known: KnownStrength,
			Setup: append(noiseFacts(30),
				said(18, "The anodising line we use is called Fuyao Line 3 and it has a four week lead time."),
			),
			Query: Query{Task: "what is the lead time on Fuyao Line 3?", Budget: 2000, Now: benchNow},
			Gold:  Gold{Carry: []Fact{{Label: "the lead time", Any: []string{"four week", "4 week"}}}},
		},
		{
			ID: "recall-graph-reach", Family: "memory", Skill: "graph-reach",
			Why:   "The relevant note shares no words with the question and is reachable only through a link the user wrote.",
			Known: KnownStrength,
			Setup: []Event{
				doc(30, "kestrel-one", "Kestrel One", "Smart glasses. Target BOM $118, actual $141.20. Margin depends on [[bonding-yield]]."),
				doc(30, "", "Bonding yield", "Display bonding runs at 71 percent first-pass. Every scrapped unit is absorbed by the units that ship."),
				doc(29, "", "Packaging", "Recycled moulded pulp tray, $1.90 per unit."),
			},
			Query: Query{Task: "why is the per-unit cost higher than the parts add up to?", Project: "kestrel-one", Budget: 2000, Now: benchNow},
			Gold: Gold{Carry: []Fact{
				{Label: "the yield note, reached by link not by wording", Any: []string{"71 percent", "71%", "scrapped"}},
			}},
		},
		{
			ID: "recall-multi-hop", Family: "memory", Skill: "multi-hop",
			Why:   "The answer needs two facts from two places; either alone is not an answer.",
			Known: KnownWeakness,
			Setup: append(noiseFacts(20),
				said(30, "Tomas runs manufacturing operations and owns every supplier relationship."),
				said(22, "The tooling freeze needs sign-off from whoever owns supplier relationships."),
			),
			Query: Query{Task: "who has to sign off the tooling freeze?", Budget: 2000, Now: benchNow},
			Gold: Gold{Carry: []Fact{
				{Label: "the person", Any: []string{"tomas"}},
				{Label: "the link that identifies them", Any: []string{"supplier relationship"}},
			}},
		},
		{
			ID: "supersession-current-value", Family: "memory", Skill: "supersession",
			Why:   "A number that changed twice. The current one must come back and the old ones must not.",
			Known: KnownStrength,
			Setup: append(noiseFacts(20),
				said(40, "We are targeting a $199 retail price."),
				said(25, "Retail is moving to $229 after the optics quote came back."),
				said(6, "Final call: retail price is $249. That is locked for launch."),
			),
			Query: Query{Task: "what is the retail price?", Budget: 2000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{{Label: "the current price", Any: []string{"$249", "249"}}},
				Avoid: []Fact{
					{Label: "the first superseded price", Any: []string{"$199", "199"}},
					{Label: "the second superseded price", Any: []string{"$229", "229"}},
				},
			},
		},

		// ---- the ones we lose ------------------------------------------------

		{
			ID: "negation-decided-against", Family: "memory", Skill: "negation",
			Why:   "'We decided not to use X' and 'we use X' sit next to each other in embedding space.",
			Known: KnownStrength,
			Setup: append(noiseFacts(20),
				said(15, "We decided against Rust for the firmware — the vendor SDK is C only and the bindings were a maintenance sink."),
			),
			Query: Query{Task: "what language is the firmware written in?", Budget: 2000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{{Label: "the actual answer", Any: []string{" c only", "vendor sdk is c"}}},
				Signal: []Fact{
					{Label: "the negation is preserved, not dropped", Any: []string{"decided against", "not use rust", "rejected rust"}},
				},
			},
		},
		{
			ID: "negation-preference", Family: "memory", Skill: "negation",
			Why:   "A negative preference retrieved for a positively-worded question reads as an endorsement.",
			Known: KnownStrength,
			Setup: append(noiseFacts(20),
				said(20, "Do not schedule me anything before 10am — I am useless in the mornings."),
			),
			Query: Query{Task: "when should I schedule the supplier call?", Budget: 2000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{{Label: "the constraint", Any: []string{"before 10am", "10am"}}},
				Signal: []Fact{
					{Label: "carried as a prohibition, not a preference for mornings", Any: []string{"do not", "don't", "avoid", "never"}},
				},
			},
		},
		{
			ID: "abstention-never-recorded", Family: "memory", Skill: "abstention",
			Why:   "Asked about something never said. Saying so is the correct answer; the LongMemEval harness filters these out before scoring.",
			Known: KnownStrength,
			Setup: append(noiseFacts(30),
				said(20, "Our contract manufacturer is Pegatron, in the Suzhou plant."),
			),
			Query: Query{Task: "what did we agree the warranty period would be?", Budget: 2000, Now: benchNow},
			Gold: Gold{
				Signal: []Fact{
					{Label: "admits it does not know", Any: []string{"nothing recorded", "no record", "not know", "nothing on", "no memories", "nothing found", "no matching", "not recorded"}},
				},
				Avoid: []Fact{
					{Label: "confabulating a neighbour as the answer", Any: []string{"warranty period is", "warranty is 1", "warranty is 2", "12 months", "24 months"}},
				},
			},
		},
		{
			ID: "abstention-adjacent", Family: "memory", Skill: "abstention",
			Why:   "The store knows the neighbouring fact. Returning it unlabelled reads as an answer to a question nobody answered.",
			Known: KnownStrength,
			Setup: append(noiseFacts(20),
				said(20, "The Suzhou plant handles final assembly."),
				said(18, "The Suzhou plant runs two shifts."),
			),
			Query: Query{Task: "which plant does the optical bonding?", Budget: 2000, Now: benchNow},
			Gold: Gold{
				Signal: []Fact{
					{Label: "flags that bonding specifically is unrecorded", Any: []string{"nothing recorded", "no record", "not know", "nothing on", "no matching", "not recorded", "unclear"}},
				},
			},
		},
		{
			ID: "numeric-aggregation", Family: "memory", Skill: "arithmetic",
			Why:   "The answer is a sum across five notes. Retrieval can surface the parts; it cannot add them.",
			Known: KnownWeakness,
			Setup: []Event{
				doc(30, "tooling", "Tooling — injection mould A", "Injection mould set A: $42,000."),
				doc(29, "tooling", "Tooling — injection mould B", "Injection mould set B: $38,500."),
				doc(28, "tooling", "Tooling — CNC fixtures", "CNC fixtures: $11,200."),
				doc(27, "tooling", "Tooling — test jigs", "Test jigs: $7,800."),
				doc(26, "tooling", "Tooling — line retooling", "Assembly line retooling: $16,500."),
			},
			Query: Query{Task: "what have we spent on tooling in total?", Project: "tooling", Budget: 2000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{{Label: "the computed total", Any: []string{"116,000", "116000", "$116"}}},
			},
		},
		{
			ID: "temporal-ordering", Family: "memory", Skill: "temporal",
			Why:   "Which came first. The store holds timestamps but the answer needs them compared, not listed.",
			Known: KnownWeakness,
			Setup: append(noiseFacts(15),
				said(35, "Signed the Pegatron manufacturing agreement today."),
				said(28, "Locked the industrial design today — no more changes to the shell."),
				said(12, "Kicked off certification prep today."),
			),
			Query: Query{Task: "did we lock the industrial design before or after signing with Pegatron?", Budget: 2000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{{Label: "the ordering, stated", Any: []string{"after signing", "after the pegatron", "pegatron first", "signed first"}}},
			},
		},
		{
			ID: "temporal-window", Family: "memory", Skill: "temporal",
			Why:   "'Last month' has to be resolved against the clock, not matched as a string.",
			Known: KnownWeakness,
			Setup: []Event{
				note(45, "user", "kestrel-one", "Spent the week on the optics quote comparison."),
				note(35, "user", "kestrel-one", "Ran the drop test series on the magnesium frame."),
				note(5, "user", "kestrel-one", "Started the packaging design review."),
			},
			Query: Query{Task: "what was I working on about five weeks ago?", Project: "kestrel-one", Budget: 2000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{{Label: "the item in the window", Any: []string{"drop test", "magnesium"}}},
				Avoid: []Fact{{Label: "the recent item, which is out of the window", Any: []string{"packaging design review"}}},
			},
		},
		{
			ID: "contradiction-unflagged", Family: "memory", Skill: "conflict",
			Why:   "Two sources disagree and neither supersedes the other. Picking one silently is the failure.",
			Known: KnownStrength,
			Setup: append(noiseFacts(15),
				doc(20, "", "Ops summary", "First-pass bonding yield is 71 percent."),
				doc(19, "", "Factory report week 34", "Bonding first-pass yield measured at 63 percent across the week."),
			),
			Query: Query{Task: "what is the bonding yield?", Budget: 2000, Now: benchNow},
			Gold: Gold{
				Carry: []Fact{
					{Label: "both figures present", All: []string{"71", "63"}},
				},
				Signal: []Fact{
					{Label: "the disagreement is named", Any: []string{"conflict", "contradict", "disagree", "two figures", "differs", "however"}},
				},
			},
		},
		{
			ID: "scale-haystack", Family: "memory", Skill: "scale",
			Why:   "Two hundred facts, one of them the answer. Precision under volume.",
			Known: KnownStrength,
			Setup: append(noiseFacts(200),
				said(9, "The hinge supplier is Sugatsune and they quoted 11 weeks for the custom detent."),
			),
			Query: Query{Task: "how long is the hinge lead time?", Budget: 2000, Now: benchNow},
			Gold:  Gold{Carry: []Fact{{Label: "the lead time", Any: []string{"11 week", "eleven week"}}}},
		},
	}
}

// ---------------------------------------------------------------------------
// Durability — what is left when the cache is gone.
// ---------------------------------------------------------------------------

func durability() []Scenario {
	return []Scenario{
		{
			ID: "durability-handoff-survives-wipe", Family: "durability", Skill: "source-of-truth",
			Why:         "Delete every derived artifact. A handoff recorded as a file survives; one recorded in an index does not.",
			Known:       KnownStrength,
			DropDerived: true,
			Setup: []Event{
				doc(30, "kestrel-one", "Kestrel One", "Smart glasses programme."),
				{
					TS: ago(2), Actor: "claude", Kind: KindCheckpoint, Project: "kestrel-one",
					Task:   "close the BOM gap",
					Failed: []string{"Re-quoting the waveguide — no movement under 10k units"},
					Next:   "Quote the display driver alternatives",
				},
			},
			Query: Query{Task: "continue the BOM work", Project: "kestrel-one", Agent: "cursor", Budget: 4000, Now: benchNow},
			Gold: Gold{Carry: []Fact{
				{Label: "the failed approach survived", Any: []string{"waveguide"}},
				{Label: "the next step survived", Any: []string{"display driver"}},
			}},
		},
		{
			ID: "durability-memories-survive-wipe", Family: "durability", Skill: "source-of-truth",
			Why:         "brain's own principle says the database is a cache. Its memories live only in that cache.",
			Known:       KnownWeakness,
			DropDerived: true,
			Setup: append(noiseFacts(10),
				said(20, "Our contract manufacturer is Pegatron, in the Suzhou plant."),
				said(18, "I prefer written proposals over meetings."),
			),
			Query: Query{Task: "who manufactures our hardware?", Budget: 2000, Now: benchNow},
			Gold: Gold{Carry: []Fact{
				{Label: "the fact survived the wipe", Any: []string{"pegatron"}},
			}},
		},
		{
			ID: "durability-notes-survive-wipe", Family: "durability", Skill: "source-of-truth",
			Why:         "Documents written as files should come back byte-identical after a rebuild.",
			Known:       KnownStrength,
			DropDerived: true,
			Setup: []Event{
				doc(30, "kestrel-one", "Kestrel One", "Smart glasses. Target BOM $118, actual $141.20."),
				doc(30, "", "Bonding yield", "Display bonding runs at 71 percent first-pass."),
			},
			Query: Query{Task: "what is the BOM gap?", Project: "kestrel-one", Budget: 2000, Now: benchNow},
			Gold: Gold{Carry: []Fact{
				{Label: "the note survived", Any: []string{"141.20", "$141"}},
			}},
		},
	}
}

// ---------------------------------------------------------------------------
// Filler. Distractors are part of the measurement: a store that returns the
// right answer from a haystack of three is not doing the job it will be asked
// to do live.
// ---------------------------------------------------------------------------

var fillerTopics = []string{
	"The staging server restarts nightly at 3am",
	"Design review notes are filed under the sprint folder",
	"Invoices go to accounts payable on the first of the month",
	"The office wifi password rotates quarterly",
	"Standup is at 9:15 in the small room",
	"Build artifacts are retained for thirty days",
	"The shared calendar is owned by operations",
	"Expense reports need a receipt over twenty dollars",
	"The parking permit renews in March",
	"Slack retention is set to one year",
}

func noise(n int, project, prefix string) []Event {
	out := make([]Event, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, note(60-i%50, "claude", project,
			fmt.Sprintf("%s (%s)", prefix, fillerTopics[i%len(fillerTopics)])))
	}
	return out
}

func noiseFacts(n int) []Event {
	out := make([]Event, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, said(60-i%50,
			fmt.Sprintf("%s, item %d.", fillerTopics[i%len(fillerTopics)], i)))
	}
	return out
}
