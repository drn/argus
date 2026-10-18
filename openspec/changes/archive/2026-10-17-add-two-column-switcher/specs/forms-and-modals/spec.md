## REMOVED Requirements

### Requirement: Task switcher status indicator

**Reason**: The task switcher modal (`TaskSwitcherModal`) is deleted. Its only consumer was the old `ctrl+j` grouped switcher, which the two-column jump switcher replaces (the Hera parent picker never used it — it renders its own `OrchPickerModal`). The jump switcher's columns are private instances of the real Tasks list and Projects rail, so they render the exact glyphs those widgets draw through the same shared status classifier, and no separate switcher row renderer exists to drift.

**Migration**: Press `ctrl+j` to open the jump switcher; its Tasks column shows each task's status with the same glyph and color as the Tasks tab list.
