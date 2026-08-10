#!/usr/bin/env bash
# Seed a synthetic vault for exercising retrieval, the secretary, and the
# business flavor against something with real texture — costs that don't close,
# a factory that misses yield, a schedule with a critical path.
#
# The persona: CTO of Kestrel Devices, shipping a $249 pair of smart glasses.
# Everything here is invented. It exists so the assistant can be asked hard
# questions ("why is the BOM over?") that have findable answers spread across
# several notes, which is the only way to tell retrieval from autocomplete.
#
#   ./scripts/seed-demo-vault.sh [vault-dir]     # default ~/vaults/kestrel
#
# Destructive: wipes the target's notes and re-seeds. Never point it at a real
# vault — it refuses anything that isn't empty or previously seeded by this
# script (marked by .brain/demo-seed).
set -euo pipefail

VAULT="${1:-$HOME/vaults/kestrel}"
MARKER="$VAULT/.brain/demo-seed"

if [ -e "$VAULT" ] && [ ! -e "$MARKER" ] && [ -n "$(ls -A "$VAULT" 2>/dev/null)" ]; then
  echo "refusing: $VAULT is non-empty and wasn't seeded by this script." >&2
  echo "delete it yourself, or pick another path." >&2
  exit 1
fi

rm -rf "$VAULT"
mkdir -p "$VAULT"/{people,projects,topics,daily,business,.brain}
touch "$MARKER"

w() { mkdir -p "$(dirname "$VAULT/$1")"; cat > "$VAULT/$1"; }

# ---------------------------------------------------------------- people

w people/pragun.md <<'EOF'
---
type: person
title: Pragun
aliases: [me, the CTO]
relations:
  - { pred: works_on, obj: "[[kestrel-one]]", conf: 1.0, src: stated }
  - { pred: role_at, obj: "[[kestrel-devices]]", conf: 1.0, src: stated }
first_seen: 2026-05-04
---
CTO and co-founder of [[kestrel-devices]]. Owns hardware, firmware, and the
manufacturing relationship. Splits the week roughly: Monday and Tuesday on
[[waveguide-module]] and cost, Wednesday on firmware review, Thursday on the
[[sunrise-precision]] call, Friday on whatever is on fire.

Standing position: we do not ship a $249 pair of glasses that costs $141 to
build. Either the [[bom-cost]] closes or the price moves, and moving the price
kills the whole thesis. Would rather cut the camera than the display.

Hates being handed a number without the method that produced it.
EOF

w people/dana-whitfield.md <<'EOF'
---
type: person
title: Dana Whitfield
relations:
  - { pred: role_at, obj: "[[kestrel-devices]]", conf: 1.0, src: stated }
  - { pred: works_on, obj: "[[kestrel-one]]", conf: 1.0, src: stated }
first_seen: 2026-05-04
---
CEO and co-founder. Came from consumer audio, so reads the market better than
the BOM. Committed publicly to a **$249 retail price** at the March board
meeting, which is the constraint everything else now bends around.

Pushing hard to hold the November launch for holiday retail. Believes slipping
to Q1 costs us the entire first-year volume, and therefore the unit cost curve.
Tension with [[pragun]] is productive but real: Dana will trade margin for
date, [[pragun]] will trade date for yield.
EOF

w people/marcus-oyelaran.md <<'EOF'
---
type: person
title: Marcus Oyelaran
relations:
  - { pred: role_at, obj: "[[kestrel-devices]]", conf: 1.0, src: stated }
  - { pred: owns, obj: "[[tooling-and-molds]]", conf: 1.0, src: stated }
  - { pred: works_with, obj: "[[wei-lin]]", conf: 0.9, src: stated }
first_seen: 2026-05-11
---
VP Manufacturing. Twelve years at a tier-one contract manufacturer before this,
which is why he is the only one who reads [[sunrise-precision]]'s yield reports
without getting optimistic.

Owns [[tooling-and-molds]] and the [[evt-dvt-pvt]] schedule. His standing
warning: the front frame steel tool is **14 weeks** from PO to first shot, and
we have not cut the PO. Every week we spend arguing about [[bom-cost]] is a
week off the launch date, not off the tooling schedule.
EOF

w people/priya-raghunathan.md <<'EOF'
---
type: person
title: Priya Raghunathan
relations:
  - { pred: role_at, obj: "[[kestrel-devices]]", conf: 1.0, src: stated }
  - { pred: owns, obj: "[[waveguide-module]]", conf: 1.0, src: stated }
first_seen: 2026-05-11
---
Optics lead. Owns [[waveguide-module]], which is both the reason the product is
good and the reason [[bom-cost]] does not close — the surface-relief waveguide
is $34.50 a unit at our volume, nearly a third of the whole BOM.

Ran the [[waveguide-vs-birdbath]] study in June. Her conclusion was that
birdbath saves $19 and costs us the form factor, and that if we ship a birdbath
we are shipping the same glasses as everyone else. [[pragun]] agreed. [[dana-whitfield]]
has re-opened it twice since.

Currently chasing the eyebox uniformity failure that is driving the bonding
station [[yield-rate]] problem.
EOF

w people/tomas-beck.md <<'EOF'
---
type: person
title: Tomas Beck
relations:
  - { pred: role_at, obj: "[[kestrel-devices]]", conf: 1.0, src: stated }
  - { pred: owns, obj: "[[thermal-envelope]]", conf: 0.9, src: stated }
first_seen: 2026-05-18
---
Firmware lead. Owns the SoC bring-up and, by accident of who noticed it first,
[[thermal-envelope]].

Found the [[thermal-throttling]] cliff in the EVT units: sustained camera
capture drives the temple arm to 47°C in about nine minutes, past the skin
contact limit, and the SoC downclocks hard. His fix is a duty-cycle cap in
firmware, which works but silently makes the camera worse than the spec sheet
says. Wants a decision from [[pragun]] about whether we change the spec or the
hardware.
EOF

w people/wei-lin.md <<'EOF'
---
type: person
title: Wei Lin
relations:
  - { pred: role_at, obj: "[[sunrise-precision]]", conf: 1.0, src: stated }
  - { pred: works_with, obj: "[[marcus-oyelaran]]", conf: 0.9, src: stated }
first_seen: 2026-06-01
---
Program manager at [[sunrise-precision]], our contract manufacturer in Dongguan.
Runs the Thursday call. Straight with us about the [[yield-rate]] on the display
bonding station, which not every CM would be.

Her position on the EVT build: the 71% bonding yield is an active-alignment
process problem, not an operator problem, and throwing a second shift at it
makes scrap faster rather than making yield better. She wants two more weeks of
process development before the DVT build. That request is what puts the November
launch at risk.
EOF

w people/elena-vasquez.md <<'EOF'
---
type: person
title: Elena Vasquez
relations:
  - { pred: invested_in, obj: "[[kestrel-devices]]", conf: 1.0, src: stated }
first_seen: 2026-05-04
---
Partner at Ridgeline Ventures, led our Series A and sits on the board. Asks
exactly one question at every board meeting: what is the gross margin at
100k units, and when does it stop being a guess.

The August board deck has to answer that honestly. Current answer at 100k is
thin, and the honest version depends on whether the [[waveguide-module]] cost
comes down at volume or we eat it.
EOF

w people/sam-okafor.md <<'EOF'
---
type: person
title: Sam Okafor
relations:
  - { pred: role_at, obj: "[[kestrel-devices]]", conf: 1.0, src: stated }
first_seen: 2026-05-18
---
Industrial design. Fought for and won the 43g target and the single-piece front
frame, which is why [[tooling-and-molds]] is expensive and why the glasses do
not look like a science project.

Will not accept the thicker temple arm that would solve [[thermal-throttling]]
by mass alone. His argument: the moment these read as "tech glasses" instead of
"glasses," the $249 price stops being a bargain and starts being a gadget.
EOF

# ---------------------------------------------------------------- orgs

w topics/kestrel-devices.md <<'EOF'
---
type: org
title: Kestrel Devices
aliases: [the company, Kestrel]
first_seen: 2026-05-04
---
Consumer hardware company, 31 people, Series A from [[elena-vasquez]] at
Ridgeline. One product: [[kestrel-one]].

The thesis is a single sentence — smart glasses have failed at $1,500 and will
succeed at $249 — and every hard decision in the vault is downstream of it.
Burn is roughly $780k/month. Runway to March 2027, which means the November
launch is not really optional.
EOF

w topics/sunrise-precision.md <<'EOF'
---
type: org
title: Sunrise Precision
aliases: [the factory, the CM, Dongguan]
relations:
  - { pred: manufactures, obj: "[[kestrel-one]]", conf: 1.0, src: stated }
first_seen: 2026-06-01
---
Our contract manufacturer, Dongguan. Mid-tier — not Foxconn, which is why they
took a 31-person company seriously. [[wei-lin]] is our program manager.

Three lines matter to us: SMT, display bonding (active alignment), and final
assembly. The bonding station is the constraint; see [[yield-rate]]. Their
quoted NRE is $410k including [[tooling-and-molds]], and their PVT-to-volume
ramp is quoted at six weeks, which [[marcus-oyelaran]] thinks is optimistic by
about half.

Landed cost from Dongguan carries a 7.5% duty line — see [[tariffs-and-landed-cost]].
EOF

# ---------------------------------------------------------------- projects

w projects/kestrel-one.md <<'EOF'
---
type: project
title: Kestrel One
aliases: [K1, the glasses, the product]
relations:
  - { pred: owned_by, obj: "[[pragun]]", conf: 1.0, src: stated }
  - { pred: made_by, obj: "[[sunrise-precision]]", conf: 1.0, src: stated }
  - { pred: depends_on, obj: "[[waveguide-module]]", conf: 1.0, src: stated }
  - { pred: depends_on, obj: "[[tooling-and-molds]]", conf: 1.0, src: stated }
first_seen: 2026-05-04
---
Consumer smart glasses. Monocular display, 12MP camera, open-ear audio, 43g,
six-hour mixed use. Target retail **$249**, target BOM **$118**, actual BOM
today **$141.20** — see [[bom-cost]].

Launch target is **November 12, 2026**, chosen for holiday retail. The critical
path is not the software and never has been: it is [[tooling-and-molds]] (14
week lead, PO not cut) and the display bonding [[yield-rate]] at
[[sunrise-precision]].

Three open decisions, all of them mine:
1. Does the [[waveguide-module]] stay, or does cost force [[waveguide-vs-birdbath]] back open?
2. Do we ship [[thermal-throttling]] behind a firmware duty-cycle cap and adjust the spec sheet?
3. Do we give [[wei-lin]] her two weeks of process development and slip, or hold November?

They interact. That is the whole problem — solving any one of them alone makes
another worse.
EOF

w projects/waveguide-module.md <<'EOF'
---
type: project
title: Waveguide Module
aliases: [the optics, display module]
relations:
  - { pred: owned_by, obj: "[[priya-raghunathan]]", conf: 1.0, src: stated }
  - { pred: part_of, obj: "[[kestrel-one]]", conf: 1.0, src: stated }
first_seen: 2026-05-11
---
Surface-relief grating waveguide plus a 0.13" microLED light engine. The single
most expensive thing in the product at **$34.50/unit** at 100k volume — 24% of
[[bom-cost]] on its own.

Two suppliers quoted. The incumbent holds $34.50 at 100k and offers $28.90 at
250k, which we cannot commit to. The alternate quoted $31.00 but has never
shipped a consumer program and their sample eyebox uniformity was visibly worse
at the edges.

This module is also the [[yield-rate]] problem: active alignment during bonding
is what fails, and it fails on the waveguide side, not the light engine side.
[[priya-raghunathan]] is 60/40 that it is a fixture stiffness issue rather than
anything intrinsic to the part.

Cutting this to a birdbath saves $19/unit. See [[waveguide-vs-birdbath]] for why
we have said no twice.
EOF

w projects/tooling-and-molds.md <<'EOF'
---
type: project
title: Tooling and Molds
relations:
  - { pred: owned_by, obj: "[[marcus-oyelaran]]", conf: 1.0, src: stated }
  - { pred: part_of, obj: "[[kestrel-one]]", conf: 1.0, src: stated }
first_seen: 2026-05-25
---
Eleven tools. The one that matters is the **front frame**, single-cavity
hardened steel, because [[sam-okafor]] won the single-piece frame argument and a
single-piece frame needs a real tool rather than a soft tool.

- Front frame, hardened steel: **$96k, 14 weeks** PO-to-first-shot
- Temple arms (L/R), aluminium soft tool: $34k, 6 weeks
- Hinge and 8 small parts: $71k, 5–8 weeks

The 14 weeks is the number that decides the launch. Counting back from
**November 12** through PVT, DVT, and a first-shot-to-DVT gap, the PO had to be
cut by **August 4**. It is August and it is not cut, because cutting it locks
the frame geometry and the frame geometry depends on whether
[[thermal-throttling]] forces a thicker temple arm.

See [[tooling-quotes]] for the vendor comparison. [[marcus-oyelaran]]'s view is
that a soft tool bridge for the first 5k units buys us six weeks for about $40k
of scrap, and is the least bad option nobody wants to pay for.
EOF

w projects/thermal-envelope.md <<'EOF'
---
type: project
title: Thermal Envelope
relations:
  - { pred: owned_by, obj: "[[tomas-beck]]", conf: 0.9, src: stated }
  - { pred: part_of, obj: "[[kestrel-one]]", conf: 1.0, src: stated }
first_seen: 2026-06-15
---
The EVT units run hot. Sustained 1080p capture puts the right temple arm at
**47°C in about nine minutes**; the skin-contact limit we set ourselves is 43°C
and the regulatory one is higher but not by as much as anyone would like.

Three ways out, and we have to pick one:
- **Firmware duty cycle** ([[tomas-beck]]'s fix): caps sustained capture at ~6
  minutes. Costs nothing, ships now, makes the camera worse than we advertise.
- **Thicker temple arm**: solves it with thermal mass. [[sam-okafor]] has
  refused, and it re-opens the [[tooling-and-molds]] geometry we are already
  late on.
- **Graphite spreader + copper via stitching**: about $2.10/unit onto
  [[bom-cost]] which is already $23 over target, and needs a DVT to prove.

This is genuinely three-way blocked: the cheap fix hurts the product, the good
fix hurts the schedule, and the middle fix hurts the cost.
EOF

w projects/certification.md <<'EOF'
---
type: project
title: FCC and CE Certification
relations:
  - { pred: part_of, obj: "[[kestrel-one]]", conf: 1.0, src: stated }
  - { pred: owned_by, obj: "[[marcus-oyelaran]]", conf: 0.8, src: stated }
first_seen: 2026-06-22
---
Intentional-radiator testing on the BT/Wi-Fi combo plus SAR, since it is a
head-worn device. Lab slot booked at Elmwood for **September 21**, which needs
DVT-quality units in hand by September 14.

That date is downstream of everything else in the vault. If [[wei-lin]] gets her
two weeks of process development on [[yield-rate]], DVT units land September 25
and we lose the lab slot; the next one is four weeks out, which puts
certification past the November build start. This is the real reason the
November launch is tight — not assembly, paperwork.
EOF

# ---------------------------------------------------------------- topics

w topics/bom-cost.md <<'EOF'
---
type: topic
title: BOM Cost
aliases: [bill of materials, unit cost, the BOM]
relations:
  - { pred: about, obj: "[[kestrel-one]]", conf: 1.0, src: stated }
first_seen: 2026-05-11
---
Target **$118.00**. Current **$141.20** at 100k volume. Over by **$23.20**,
which at a $249 retail and a 2x wholesale multiplier is most of the margin.

Where it goes (full detail in `business/bom-kestrel-one.csv`):
- [[waveguide-module]] optics + light engine — $34.50 (24%)
- SoC and memory — $27.80
- Camera module — $16.40
- Battery and charging — $12.30
- Frame, hinge, temples — $14.90
- Audio, mics, haptics — $11.10
- PCB, passives, flex — $9.60
- Misc, packaging, cable — $14.60

The honest read: there is no single $23 cut in here. Killing the camera saves
$16.40 and guts the product. The birdbath swap saves $19 and costs the form
factor ([[waveguide-vs-birdbath]]). Volume to 250k takes the waveguide to $28.90
and gets us most of the way, but committing to 250k of inventory is a financing
decision, not an engineering one, and that is [[dana-whitfield]] and
[[elena-vasquez]]'s call rather than mine.

Also note [[tariffs-and-landed-cost]] adds ~7.5% on top of everything here, and
the $118 target was set before anyone was modelling duty.
EOF

w topics/waveguide-vs-birdbath.md <<'EOF'
---
type: topic
title: Waveguide vs Birdbath
relations:
  - { pred: about, obj: "[[waveguide-module]]", conf: 1.0, src: stated }
first_seen: 2026-06-08
---
The recurring argument. [[priya-raghunathan]] ran the study in June.

**Birdbath**: saves **$19.00/unit**, mature supply chain, better yield on day
one. Costs 4.5mm of z-height in front of the eye, which means a visibly thicker
lens stack and a product that reads as a gadget. [[sam-okafor]] calls it "the
$19 that costs us the category."

**Waveguide** (current): 43g, looks like glasses, and is the entire reason
someone pays $249 for these rather than $99 for camera glasses. Costs $34.50 and
a 71% bonding [[yield-rate]] we have not solved.

Decided against birdbath twice — June 11 and July 2. [[dana-whitfield]] re-opens
it whenever [[bom-cost]] gets discussed, which is fair of her, because $19 is
most of the $23.20 gap. My position has not changed: if we ship a birdbath we
are competing on price with companies that are better at price than us.
EOF

w topics/yield-rate.md <<'EOF'
---
type: topic
title: Yield Rate
aliases: [yield, bonding yield]
relations:
  - { pred: about, obj: "[[sunrise-precision]]", conf: 1.0, src: stated }
first_seen: 2026-07-06
---
EVT build, 400 units. Overall yield **68%**. The stations are not equally to
blame — see `business/evt-yield.csv`.

- SMT: 97%
- Display bonding (active alignment): **71%** ← the problem
- Final assembly: 94%
- Functional test: 91%

At 71% bonding yield, every good unit carries the cost of 0.41 scrapped
[[waveguide-module]]s, and the waveguide is the most expensive part in the
product. That is roughly **$14/unit of hidden cost** that does not appear
anywhere in [[bom-cost]], which is the number I keep having to remind people
about. Fixing yield to 90% is worth more than the entire birdbath swap and does
not cost us the form factor.

[[wei-lin]] wants two weeks of process development on the alignment fixture.
[[priya-raghunathan]] thinks it is fixture stiffness. If they are right, this is
the highest-leverage two weeks available to us — and it is also the two weeks
that breaks the [[certification]] lab slot.
EOF

w topics/evt-dvt-pvt.md <<'EOF'
---
type: topic
title: EVT / DVT / PVT
aliases: [build schedule, the builds]
first_seen: 2026-06-01
---
Where we are: **EVT complete** (400 units, June 29 – July 6, 68% [[yield-rate]]).

- **DVT**: 1,200 units, planned **September 7**. Needs frozen frame geometry,
  therefore needs [[tooling-and-molds]] PO cut. Feeds [[certification]] units.
- **PVT**: 3,000 units, planned **October 12**. Proves the line, not the design.
- **MP ramp**: November 3, for a **November 12** launch.

There is no float anywhere in this. Every slip is a launch slip, which is why
the [[tooling-and-molds]] PO date of August 4 was a real date and not a
suggestion.
EOF

w topics/thermal-throttling.md <<'EOF'
---
type: topic
title: Thermal Throttling
relations:
  - { pred: about, obj: "[[thermal-envelope]]", conf: 1.0, src: stated }
first_seen: 2026-06-15
---
[[tomas-beck]] found it in EVT: nine minutes of sustained 1080p capture, right
temple hits 47°C, SoC downclocks and frame rate falls off a cliff.

The uncomfortable part is not the heat, it is the marketing copy. We have been
saying "all-day capture." A duty-cycle cap makes that false. If we ship the
firmware fix we have to change the spec sheet, and changing the spec sheet after
[[dana-whitfield]] has briefed retail buyers is its own kind of expensive.

Blocks [[tooling-and-molds]], because the thicker-temple option would change the
frame geometry we need to freeze.
EOF

w topics/tariffs-and-landed-cost.md <<'EOF'
---
type: topic
title: Tariffs and Landed Cost
relations:
  - { pred: about, obj: "[[bom-cost]]", conf: 0.9, src: stated }
first_seen: 2026-07-13
---
Assembled in Dongguan, imported to the US. Current duty line lands at **7.5%**
on declared value, plus freight at roughly $1.90/unit air and $0.40 sea.

On a $141.20 [[bom-cost]] that is about **$10.60/unit** nobody was modelling
when the $118 target was set. The target was a BOM target and we have been
quietly treating it as a landed-cost target, which is how you end up $34 in the
hole instead of $23.

Vietnam second-sourcing gets floated every time this comes up. It is real but it
is a 2027 conversation — [[sunrise-precision]] holds the bonding process
knowledge and moving it is the same two-week [[yield-rate]] problem again,
somewhere with less experience.
EOF

w topics/tooling-quotes.md <<'EOF'
---
type: topic
title: Tooling Quotes
relations:
  - { pred: about, obj: "[[tooling-and-molds]]", conf: 1.0, src: stated }
first_seen: 2026-07-20
---
Three vendors quoted the front frame steel tool. Detail in
`business/tooling-quotes.csv`.

- **Sunrise in-house**: $96k, 14 weeks. Known quantity, co-located with the line.
- **Meridian Tooling (Taichung)**: $88k, 11 weeks. Cheaper and faster, but the
  tool lives in Taiwan and the line is in Dongguan — every engineering change
  becomes a shipping problem.
- **Apex Mold (Dongguan)**: $79k, 16 weeks. Cheapest, slowest, and
  [[marcus-oyelaran]] has seen their texture work before and did not love it.

Meridian's 11 weeks is the only quote that still makes November without a soft
tool bridge. [[marcus-oyelaran]] is against it on change-management grounds and
he is usually right about this class of thing.
EOF

# ---------------------------------------------------------------- business data

w business/bom-kestrel-one.csv <<'EOF'
category,line_item,supplier,qty_per_unit,unit_cost_100k,extended_cost,target_cost,notes
Optics,SRG waveguide,Lumina Optical,1,22.40,22.40,16.00,quoted 18.90 at 250k
Optics,microLED light engine 0.13in,Lumina Optical,1,12.10,12.10,10.00,quoted 10.00 at 250k
Compute,SoC MR4100,Ambarelle,1,19.60,19.60,18.00,locked
Compute,LPDDR5 4GB,SK Group,1,5.40,5.40,5.40,commodity
Compute,eMMC 64GB,SK Group,1,2.80,2.80,2.80,commodity
Camera,12MP sensor module,Sunny Vision,1,13.90,13.90,12.00,
Camera,camera flex + shielding,Sunrise Precision,1,2.50,2.50,2.20,
Power,battery 620mAh Li-po,Highpower,2,4.85,9.70,9.00,two cells one per temple
Power,charging IC + USB-C,TI,1,2.60,2.60,2.40,
Mechanical,front frame injection,Sunrise Precision,1,6.20,6.20,5.50,tooling amortised separately
Mechanical,temple arms pair,Sunrise Precision,2,2.90,5.80,5.00,
Mechanical,hinge assembly,Kaifeng Hardware,2,1.45,2.90,2.60,
Audio,open-ear speaker,Knowles,2,3.20,6.40,6.00,
Audio,MEMS microphone,Knowles,3,0.95,2.85,2.70,
Audio,haptic actuator,AAC,1,1.85,1.85,1.60,
PCB,main flex-rigid 6L,Shennan Circuits,1,6.10,6.10,5.50,
PCB,passives and connectors,Various,1,3.50,3.50,3.20,
Other,IPD adjustment mechanism,Sunrise Precision,1,3.40,3.40,2.80,
Other,nose pads and temple tips,Sunrise Precision,1,1.10,1.10,1.00,
Other,packaging and literature,Pak Solutions,1,4.60,4.60,4.00,
Other,USB-C cable and case,Pak Solutions,1,5.50,5.50,4.60,
EOF

w business/evt-yield.csv <<'EOF'
station,units_in,units_out,yield_pct,top_defect,scrap_value_usd
SMT,400,388,97.0,solder bridge U7,1240
Display bonding,388,276,71.1,active alignment eyebox uniformity,14820
Final assembly,276,259,93.8,hinge torque out of spec,980
Functional test,259,236,91.1,camera focus calibration,3110
EOF

w business/unit-economics.csv <<'EOF'
volume_units,bom_cost,yield_loss_per_unit,landed_duty_freight,total_cogs,retail_price,wholesale_price,gross_margin_wholesale,gross_margin_pct
25000,152.40,21.30,11.90,185.60,249.00,137.00,-48.60,-35.5
50000,146.80,17.20,11.40,175.40,249.00,137.00,-38.40,-28.0
100000,141.20,14.00,10.60,165.80,249.00,137.00,-28.80,-21.0
250000,128.60,6.80,9.80,145.20,249.00,137.00,-8.20,-6.0
500000,121.40,4.10,9.40,134.90,249.00,137.00,2.10,1.5
EOF

w business/tooling-quotes.csv <<'EOF'
vendor,location,tool,material,cavities,quote_usd,lead_weeks,first_shot_date,notes
Sunrise Precision,Dongguan,front frame,hardened steel P20,1,96000,14,2026-11-10,co-located with assembly line
Meridian Tooling,Taichung,front frame,hardened steel P20,1,88000,11,2026-10-20,tool offshore from line
Apex Mold,Dongguan,front frame,hardened steel P20,1,79000,16,2026-11-24,texture quality concerns
Sunrise Precision,Dongguan,temple arms,aluminium soft,2,34000,6,2026-09-15,
Sunrise Precision,Dongguan,hinge + smalls,steel,8,71000,8,2026-09-29,
EOF

# ---------------------------------------------------------------- daily notes

w daily/2026-07-20.md <<'EOF'
---
type: daily
date: 2026-07-20
sessions: 6
---

- Reviewed the EVT [[yield-rate]] report with [[marcus-oyelaran]]. Bonding at 71% is worse than the 82% we planned around, and the scrap is landing on the most expensive part in the product.
- [[priya-raghunathan]] walked through the eyebox uniformity failures. Her read is fixture stiffness rather than the waveguide part itself — 60/40, not certain.
- [[dana-whitfield]] re-opened [[waveguide-vs-birdbath]] in the afternoon, third time. Same $19, same answer.
- Pulled the [[tooling-quotes]] together. Meridian at 11 weeks is the only one that still makes November unassisted.

## Observations

- 09:10–10:40 (90m) yield review
- 11:00–12:15 (75m) optics deep dive
- 14:00–14:45 (45m) cost argument
- 16:20–17:50 (90m) tooling comparison
EOF

w daily/2026-07-23.md <<'EOF'
---
type: daily
date: 2026-07-23
sessions: 5
---

- Thursday call with [[wei-lin]]. She asked directly for two weeks of process development on the alignment fixture before DVT. Did not commit — that request breaks the [[certification]] lab slot on September 21.
- [[tomas-beck]] demoed the duty-cycle fix for [[thermal-throttling]]. It works. It also means "all-day capture" is not true and the spec sheet has to change.
- Modelled the yield cost properly for the first time: at 71% bonding, scrap adds about $14/unit that never appears in [[bom-cost]].
- Note to self: fixing yield is worth more than the birdbath swap and costs us nothing in form factor. Keep saying this until it lands.

## Observations

- 08:00–09:00 (60m) Sunrise call
- 09:30–11:00 (90m) thermal review
- 13:00–15:30 (150m) cost modelling
EOF

w daily/2026-07-28.md <<'EOF'
---
type: daily
date: 2026-07-28
sessions: 7
---

- [[marcus-oyelaran]] escalated the [[tooling-and-molds]] PO. Front frame is 14 weeks and the drop-dead date to cut it is August 4. We cannot cut it while [[thermal-envelope]] might force a thicker temple arm.
- [[sam-okafor]] refused the thicker temple again. His argument is good: at 43g and glasses-shaped we are a bargain, at 60g and gadget-shaped we are a toy.
- Started the graphite spreader estimate as the middle path — about $2.10/unit onto a BOM already $23 over.
- [[elena-vasquez]] emailed about the August board deck. She wants gross margin at 100k and she wants it to stop being a guess.

## Observations

- 08:45–10:15 (90m)
- 10:30–11:15 (45m) ID review
- 13:15–15:00 (105m) thermal options
- 15:30–16:00 (30m) board prep
- 16:30–18:20 (110m)
EOF

w daily/2026-08-03.md <<'EOF'
---
type: daily
date: 2026-08-03
sessions: 6
---

- One day to the [[tooling-and-molds]] PO deadline and the frame geometry is still not frozen. This is the week it stops being recoverable.
- Ran the [[unit-economics]] properly across volumes. We do not reach positive wholesale margin until 500k units, which is not a number we can finance. At 100k we are 21% underwater.
- That reframes everything: the problem is not $23 of [[bom-cost]], it is that the $249 price was set against a BOM target that never included yield loss or [[tariffs-and-landed-cost]].
- Have to bring this to [[dana-whitfield]] before the board deck, not after.

## Observations

- 09:00–11:30 (150m) unit economics
- 13:00–14:00 (60m)
- 14:30–16:45 (135m) board deck draft
EOF

w daily/2026-08-06.md <<'EOF'
---
type: daily
date: 2026-08-06
sessions: 5
---

- Missed the August 4 [[tooling-and-molds]] PO date. [[marcus-oyelaran]] is now costing the soft-tool bridge — roughly $40k of scrap to buy six weeks.
- [[wei-lin]] sent the fixture stiffness data. [[priya-raghunathan]] was right: deflection under clamp load is the alignment failure. That is fixable in about two weeks, which is exactly what Wei asked for on the 23rd.
- So the two weeks are real and they are worth ~$14/unit. The question is whether [[certification]] can move, not whether the two weeks are justified.
- Called Elmwood about the September 21 lab slot. Waiting on whether they can hold a late-September alternate.

## Observations

- 08:30–10:00 (90m)
- 10:15–11:45 (90m) fixture data
- 14:00–15:20 (80m)
- 16:00–17:10 (70m) cert logistics
EOF

w daily/2026-08-07.md <<'EOF'
---
type: daily
date: 2026-08-07
sessions: 4
---

- Board deck for [[elena-vasquez]] is due Monday. The honest version says November is at risk and 100k margin is negative. The comfortable version says neither. Writing the honest one.
- [[dana-whitfield]] and I finally had the real conversation: the $249 was committed publicly before we understood yield or duty. Neither of us wants to move it. One of us will have to.
- [[tomas-beck]] is holding the [[thermal-throttling]] decision pending frame geometry, which is pending the PO, which is pending thermal. Circular. Someone has to break it and it is me.

## Observations

- 09:00–10:30 (90m) board deck
- 11:00–12:00 (60m) with Dana
- 14:30–16:30 (120m)
EOF

# ---------------------------------------------------------------- config

cat > "$VAULT/.brain/flavor.json" <<'EOF'
{
  "active": "business",
  "name": "Kestrel",
  "user_name": "Pragun",
  "onboarded": true,
  "presence": {
    "interjections": true,
    "wake_word": false,
    "meeting_lead_minutes": 10,
    "min_gap_minutes": 60
  }
}
EOF

# Open loops live in the cache, not in markdown, so the secretary's brief is
# empty without them — seed a few so `brain brief` has something to say.
BRAIN="${BRAIN_BIN:-./bin/brain}"
if [ -x "$BRAIN" ]; then
  while IFS= read -r loop; do
    [ -n "$loop" ] && BRAIN_VAULT="$VAULT" "$BRAIN" loop add "$loop" >/dev/null
  done <<'LOOPS'
Cut the front frame tooling PO — 14 week lead, already past the Aug 4 drop-dead date
Send Elena the August board deck with the honest 100k margin number
Decide thermal: firmware duty cycle, thicker temple, or graphite spreader — it blocks the frame freeze
Call Elmwood back about holding a late-September cert lab slot
Give Wei Lin an answer on the two weeks of fixture process development
Tell Dana the $249 was set against a BOM target that never included yield loss or duty
LOOPS
  echo "  open loops seeded"
else
  echo "  (no ./bin/brain — skipped open loops; set BRAIN_BIN to seed them)"
fi

echo "seeded $VAULT"
find "$VAULT" \( -name '*.md' -o -name '*.csv' \) | wc -l | xargs echo "  files:"
echo
echo "next:"
echo "  BRAIN_VAULT=$VAULT ./bin/brain index"
echo "  BRAIN_VAULT=$VAULT ./bin/brain ask 'why is the BOM over target?'"
