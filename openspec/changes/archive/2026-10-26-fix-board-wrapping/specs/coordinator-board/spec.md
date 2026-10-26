## MODIFIED Requirements

### Requirement: Human board interaction

The TUI SHALL render collapsed entries with bold kind/id/subject headers, support j/k and arrows, Enter expansion marking read, r reply and x dismissal. Reply SHALL focus the coordinator and paste the subject/id followed by two newlines with user input origin, without submitting. Decision dismissal SHALL durably notify its coordinator; resolution SHALL record the human as resolver.

Collapsed headers SHALL truncate with a trailing ellipsis when necessary, reserving space for the unread ` •` marker. Expanded headers SHALL show the full subject wrapped at word boundaries with continuation lines aligned under the subject's first character; all header lines SHALL be bold and only the selected first line SHALL be reverse-selected. The unread marker SHALL remain visible on the first header line. Indentation SHALL shrink in panes too narrow for the prefix.

Bodies SHALL wrap at word boundaries in display columns, preserve interior blank lines, and hard-break only tokens longer than the available line. Paired `**bold**` spans SHALL render bold without markers. Lines beginning `- ` or `* ` SHALL wrap with a hanging indent under the bullet text. Unknown syntax SHALL stay literal. Wide runes SHALL not overwrite borders. Scrolling and cursor follow SHALL account for every wrapped header and body line while keeping the selected header visible after navigation.

#### Scenario: Reply composition

- **WHEN** Aaron presses r on an item in the focused board
- **THEN** the coordinator composer contains the reply prefix with an empty line ready for typing and no submission occurs

#### Scenario: Decision dismissal

- **WHEN** Aaron dismisses a decision
- **THEN** it is resolved by human and the coordinator inbox durably records that it was dropped
- **AND** resolution and notice commit atomically; the unread notice is delivered through the normal notifier as a bridge plugin-framed turn or PTY fallback, labelled `Aaron (board)` in doorbells, inbox/history and bridge mail UI

#### Scenario: Dismissal before binding or notification enqueue

- **WHEN** a human decision dismissal commits before the coordinator is bound or the process exits before enqueue
- **THEN** the pending notice remains durable and the notification tick delivers it after a live binding exists; repeated dismissal creates no second notice

#### Scenario: Expanded readable content

- **WHEN** an expanded item contains a long subject, ordinary words, blank lines, an overlong token and wide runes
- **THEN** its full bold subject hangs under the first subject character, its body wraps words while retaining blank lines and splitting only the overlong token, and wide runes stay inside the panel

#### Scenario: Bold and bullet body

- **WHEN** a body contains paired bold spans, bullet lines, unmatched markers and other markdown syntax
- **THEN** paired bold spans lose their markers and paint bold across wraps, bullet continuations align under their text, and unmatched markers and unknown syntax remain literal

#### Scenario: Collapsed unread subject

- **WHEN** an unread collapsed item's subject exceeds the panel width
- **THEN** the subject ends with an ellipsis followed by a visible unread marker

#### Scenario: Cursor follows wrapped headers

- **WHEN** a wrapped expanded header pushes another item's selected header below the visible region and Aaron navigates to it
- **THEN** the selected header scrolls into view above the reserved hint row, and navigating back reveals the expanded item's selected first header line
