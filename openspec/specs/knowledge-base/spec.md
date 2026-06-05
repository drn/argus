# Knowledge Base

## Purpose

The knowledge base provides a searchable, full-text index over a vault of Obsidian-style markdown documents. It parses each markdown file's YAML frontmatter and body into a structured document, keeps the index continuously synchronized with the files on disk, and exposes search, list, ingest, and status operations so users and agents can find and retrieve stored knowledge.

## Requirements

### Requirement: Markdown document parsing

The system SHALL parse a markdown file into a document with a title, tag list, body, word count, and tier. The title SHALL be resolved by precedence: YAML frontmatter `title`, then the first H1 heading in the body, then the filename stem (without the `.md` extension). The body SHALL exclude any frontmatter block. Newly parsed documents SHALL be assigned the tier `hot`.

#### Scenario: Title from frontmatter

- **WHEN** a document contains YAML frontmatter with a `title` field
- **THEN** the parsed document's title is taken from that field and the body excludes the frontmatter block

#### Scenario: Title falls back to first H1

- **WHEN** a document has no frontmatter title but the body contains an H1 heading
- **THEN** the parsed document's title is the text of that H1 heading

#### Scenario: Title falls back to filename stem

- **WHEN** a document has neither a frontmatter title nor an H1 heading
- **THEN** the parsed document's title is the filename with the `.md` extension stripped

#### Scenario: Default tier and word count

- **WHEN** a document is parsed
- **THEN** its tier is `hot` and its word count reflects the number of words in the body

### Requirement: YAML frontmatter extraction

The system SHALL extract `title` and `tags` from a leading YAML frontmatter block delimited by `---` lines, supporting both LF and CRLF line endings. Tags MAY be expressed inline as a bracketed or comma-separated list. When the opening `---` has no matching closing `---`, the system SHALL treat the file as having no frontmatter and return the original content as the body.

#### Scenario: Inline tag list

- **WHEN** frontmatter contains `tags: [alpha, beta]`
- **THEN** the parsed tags are `alpha` and `beta`

#### Scenario: No frontmatter present

- **WHEN** a document does not begin with a `---` delimiter
- **THEN** no title or tags are extracted and the body is the full content unchanged

#### Scenario: Malformed frontmatter with no closing delimiter

- **WHEN** a document opens with `---` but never closes the block
- **THEN** no title or tags are extracted and the body is returned as the full original content unchanged

#### Scenario: CRLF line endings

- **WHEN** the frontmatter block uses `\r\n` line endings
- **THEN** the title and body are still extracted correctly

### Requirement: Markdown rendering round-trip

The system SHALL render a document back into markdown with a YAML frontmatter block containing the title and, when present, the tags. Titles containing double quotes SHALL be escaped, and tags containing YAML-special characters (comma, closing bracket, quote, backslash) SHALL be quoted. The rendered output SHALL end with a trailing newline. A render followed by a parse SHALL preserve the title, tag count, and body content.

#### Scenario: Render with tags

- **WHEN** a document with a title and tags is rendered
- **THEN** the output contains the title and tags in the frontmatter and ends with a newline

#### Scenario: Render without tags

- **WHEN** a document has no tags
- **THEN** the rendered frontmatter contains no `tags` line

#### Scenario: Special characters are escaped

- **WHEN** the title contains a double quote or a tag contains a comma or quote
- **THEN** the rendered frontmatter escapes the quote in the title and quotes the affected tags

#### Scenario: Round-trip preserves content

- **WHEN** a document is rendered to markdown and that markdown is parsed back
- **THEN** the parsed title, tag count, and body content match the original document

### Requirement: File ingestion into the index

The system SHALL ingest a markdown file by reading its contents, parsing it into a document, and upserting it into the store keyed by its vault-relative path. The stored path SHALL be the file's path relative to the configured vault root; if a relative path cannot be computed, the original (absolute) path SHALL be used as a fallback. Ingestion SHALL record an ingestion timestamp and a modification timestamp.

#### Scenario: Ingest a file under the vault

- **WHEN** a markdown file inside the vault is ingested
- **THEN** the store contains a document keyed by the file's vault-relative path with the parsed title, tags, and body

#### Scenario: Missing file fails ingestion

- **WHEN** ingestion targets a path that does not exist on disk
- **THEN** ingestion returns an error and no document is stored

#### Scenario: Relative-path fallback

- **WHEN** the vault-relative path cannot be computed for an ingested file
- **THEN** the document is stored under its original path

### Requirement: Full scan of the vault

The system SHALL walk the entire vault and ingest every markdown file. It SHALL only process files whose name ends in `.md` (case-insensitive), and SHALL skip hidden directories (those whose name begins with `.`, such as `.obsidian`, `.git`, `.trash`) and their contents. If the vault root itself is inaccessible, the scan SHALL return an error; errors reading individual sub-paths SHALL be skipped without failing the scan.

#### Scenario: Only markdown files in non-hidden directories are indexed

- **WHEN** a full scan runs over a vault containing markdown files, a non-markdown file, a nested markdown file, and a markdown file inside a hidden directory
- **THEN** the markdown files in non-hidden directories (including nested ones) are indexed and the non-markdown file and the hidden-directory file are not

#### Scenario: Inaccessible vault root errors

- **WHEN** a full scan targets a vault path that does not exist
- **THEN** the scan returns an error

### Requirement: Incremental synchronization

The system SHALL synchronize the index against the vault given a map of indexed paths to their stored modification times. For each markdown file on disk, the system SHALL re-ingest it only if it is new or its modification time differs from the stored value, and SHALL skip files whose modification time is unchanged. For each stored path no longer present on disk, the system SHALL delete it from the index; a delete failure SHALL be logged and SHALL NOT abort the synchronization.

#### Scenario: Unchanged file is skipped

- **WHEN** an incremental scan encounters a file whose disk modification time equals the stored modification time
- **THEN** the file is not re-ingested and its stored document is left untouched

#### Scenario: Modified file is re-ingested

- **WHEN** an incremental scan encounters a file whose disk modification time differs from the stored value
- **THEN** the file is re-ingested with its updated content

#### Scenario: New file is ingested

- **WHEN** an incremental scan encounters a markdown file not present in the stored metadata
- **THEN** the file is ingested into the index

#### Scenario: Removed file is deleted

- **WHEN** a stored path is absent from the vault on disk during an incremental scan
- **THEN** that path is deleted from the index

### Requirement: Continuous file watching

After an initial scan, the system SHALL watch the vault recursively for file changes and keep the index synchronized. Creating or writing an eligible markdown file SHALL ingest it after a debounce delay; removing or renaming an eligible markdown file SHALL delete it from the index. Newly created subdirectories SHALL be added to the watch (excluding hidden directories). Files that are hidden, end in `.icloud`, end in `.tmp`, or are not `.md` SHALL NOT be ingested.

#### Scenario: New file is picked up

- **WHEN** a markdown file is created in the watched vault after the watcher is ready
- **THEN** the file is ingested into the index after the debounce delay

#### Scenario: Deleted file is removed

- **WHEN** a watched markdown file is removed from disk
- **THEN** the corresponding document is deleted from the index

#### Scenario: New subdirectory is watched

- **WHEN** a new non-hidden subdirectory is created and a markdown file is added inside it
- **THEN** the nested file is ingested into the index

#### Scenario: Ineligible files are ignored

- **WHEN** a non-markdown file (or a `.icloud`, `.tmp`, or hidden file) is created in the vault
- **THEN** it is not ingested into the index

### Requirement: Startup scan strategy

On start, the system SHALL choose a scan strategy based on whether the index already holds documents. If the index is non-empty, it SHALL run a synchronous incremental scan against the existing metadata. If the index is empty, it SHALL run a full scan in the background so startup is not blocked, exposing a flag that reports whether the background scan is still in progress. If no vault path is configured, start SHALL be a no-op that immediately signals readiness. The system SHALL support being stopped, and stopping SHALL be idempotent.

#### Scenario: Empty index triggers background full scan

- **WHEN** start runs against an empty index with a populated vault
- **THEN** a background full scan runs, the scanning flag is set while it runs, and the vault's documents are present once it completes

#### Scenario: Non-empty index triggers synchronous incremental scan

- **WHEN** start runs against an index that already contains documents
- **THEN** an incremental scan runs synchronously and the scanning flag is not set

#### Scenario: No configured vault path

- **WHEN** start runs with an empty vault path
- **THEN** it returns without error and immediately signals readiness

#### Scenario: Metadata lookup failure aborts start

- **WHEN** start cannot read the index metadata map
- **THEN** start returns an error

#### Scenario: Stop is idempotent

- **WHEN** stop is called more than once
- **THEN** subsequent calls are no-ops and do not error

### Requirement: Search query sanitization

The system SHALL sanitize search queries before they reach the full-text index by replacing FTS5 special characters (`"`, `*`, `(`, `)`, `:`, `^`, `{`, `}`, `-`, `+`) with spaces and trimming surrounding whitespace, so that user-supplied punctuation cannot break the query.

#### Scenario: Special characters are neutralized

- **WHEN** a query contains FTS5 special characters such as `:`, `*`, `^`, or parentheses
- **THEN** those characters are replaced with spaces in the sanitized query

#### Scenario: Surrounding whitespace is trimmed

- **WHEN** a query has leading or trailing whitespace
- **THEN** the sanitized query has that whitespace removed

### Requirement: Command-line access to the knowledge base

The system SHALL provide a command-line interface to search, list, ingest, and report status of the knowledge base, communicating with the running daemon. Search SHALL accept a query and an optional result limit (default 10) and print each result's tier, title, path, and snippet, or report when no results are found. List SHALL accept an optional path prefix and limit (default 100) and print each document's path, tier, and word count, or report when none are found. Ingest SHALL read a file from disk and submit its content. Status SHALL report the indexed document count, the vault path, and the MCP port, indicating when the vault or port is not configured.

#### Scenario: Search prints results

- **WHEN** the user runs a search that returns matches
- **THEN** each match is printed with its tier, title, path, and snippet

#### Scenario: Search with no matches

- **WHEN** a search returns no results
- **THEN** the command reports that no results were found

#### Scenario: Ingest a missing file

- **WHEN** the user runs ingest against a file path that cannot be read
- **THEN** the command reports a read error and exits non-zero

#### Scenario: Status reports unconfigured vault and port

- **WHEN** status runs and no vault path or MCP port is configured
- **THEN** the output indicates the vault is not configured and the port is not running
