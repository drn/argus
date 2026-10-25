## Context

Display consumers resolve uncertain permissions with scraper corroboration. Diagnostics must retain the raw disagreement signal, while delivery writes into the session and requires a conservative permission gate.

## Goals / non-goals

- Preserve raw permission diagnostic disagreements outside the headline counters.
- Block delivery and claim-settlement absence while raw needs-input is set.
- Keep the existing per-field display rule and source logging unchanged.
- Non-goal: changing the raw idle count in `/api/system`; that metrics parity gap remains a named follow-up.

## Decisions

- Compare raw `st.NeedsInput` in the uncertain permission diagnostic bucket; retain resolved quiescence and normal headline comparisons. Comparing resolved input in that bucket merely compares the scraper with itself and cannot reveal disagreements.
- Read `bridgeState` for both delivery routing and transcript-absence settlement. Do not use display corroboration to authorise writes into the session.
- Exercise delivery with bridge idle, uncertain raw permission true, and scraper input false. This isolates the permission gate from the separate busy gate.

## Risks / trade-offs

- A real permission dialog the scraper fails to recognise (for example, new wording) is suppressed by display corroboration: while bridge busy remains true it never becomes idle and never pushes needs-attention. This is an accepted false-negative trade-off; the raw shadow permission disagreement remains visible, and delivery still blocks conservatively.
- An approved tool may keep raw permission input set until completion, delaying delivery. This is intentional because delivery writes into the session.
